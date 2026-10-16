package teamlink

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// gateTurns runs every turn in the teammate's one session behind a real
// TurnGate. busy stands for a turn of another conversation (a person
// chatting with the teammate); a task's turn runs until StopTask stops it
// or finish lets it end.
type gateTurns struct {
	gate   *TurnGate
	busy   atomic.Bool
	finish chan struct{}

	mu      sync.Mutex
	started []a2a.TaskID
	stops   map[a2a.TaskID]chan struct{}
	stopped []a2a.TaskID
}

func newGateTurns() *gateTurns {
	g := &gateTurns{finish: make(chan struct{}), stops: map[a2a.TaskID]chan struct{}{}}
	g.gate = NewTurnGate(func(string) bool { return g.busy.Load() })
	g.gate.poll = 2 * time.Millisecond
	return g
}

func (g *gateTurns) MainSession(_ context.Context, agent Peer) string { return "sess-" + agent.ID }

func (g *gateTurns) Run(ctx context.Context, agent Peer, _ string) (string, string, error) {
	session, id := "sess-"+agent.ID, TaskIDFrom(ctx)
	release, err := g.gate.Acquire(ctx, session, id)
	if err != nil {
		return session, "", err
	}
	defer release()
	stop := make(chan struct{})
	g.mu.Lock()
	g.started = append(g.started, id)
	g.stops[id] = stop
	g.mu.Unlock()
	select {
	case <-stop:
		return session, "partial work, too late", nil
	case <-g.finish:
		return session, "done " + string(id), nil
	}
}

func (g *gateTurns) StopTask(_ context.Context, _ Peer, sessionID string, id a2a.TaskID, _ string) (TaskStop, error) {
	out := g.gate.Stop(sessionID, id)
	if out == TaskStopRunning {
		g.mu.Lock()
		g.stopped = append(g.stopped, id)
		close(g.stops[id])
		g.mu.Unlock()
	}
	return out, nil
}

func (g *gateTurns) snapshot() (started, stopped []a2a.TaskID) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]a2a.TaskID(nil), g.started...), append([]a2a.TaskID(nil), g.stopped...)
}

// waitFor polls cond until it holds, failing the test after 2s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); !cond(); time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// waiting reports whether task id waits in sessionID's line.
func (g *TurnGate) waiting(sessionID string, id a2a.TaskID) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if s := g.sessions[sessionID]; s != nil {
		for _, w := range s.waiting {
			if w.id == id {
				return true
			}
		}
	}
	return false
}

func newGateHub(t *testing.T) (*Hub, *gateTurns, *fakeNotify) {
	t.Helper()
	h, _, note := newTestHub(func(Peer, string) string { return "" })
	turns := newGateTurns()
	h.Turns = turns
	return h, turns, note
}

func sendAsync(t *testing.T, h *Hub, text string) *Result {
	t.Helper()
	res, err := h.Send(context.Background(), SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: text, Wait: -1})
	if err != nil || res.State != "working" {
		t.Fatalf("send %q = %+v, %v", text, res, err)
	}
	return res
}

// Canceling the task whose turn is running stops that turn, and what the
// turn still says afterwards — its last words, a late follow-up — never
// reaches the sender.
func TestCancelStopsTheTasksOwnTurn(t *testing.T) {
	h, turns, note := newGateHub(t)
	ctx := context.Background()
	res := sendAsync(t, h, "long scan")
	waitFor(t, "the task's turn", func() bool { s, _ := turns.snapshot(); return len(s) == 1 })

	got, err := h.CancelTask(ctx, "a-cap", res.TaskID)
	if err != nil || got.State != "canceled" || got.Reason != "canceled by @captain"+stoppedNote {
		t.Fatalf("cancel = %+v, %v", got, err)
	}
	if _, stopped := turns.snapshot(); len(stopped) != 1 || string(stopped[0]) != res.TaskID {
		t.Fatalf("stopped = %v", stopped)
	}
	h.FollowUp(ctx, "sess-a-anton", "one more thing")
	time.Sleep(30 * time.Millisecond)
	if delivered, _ := note.snapshot(); len(delivered) != 0 {
		t.Fatalf("a canceled task still delivered: %v", delivered)
	}
	if again, _ := h.GetTask(ctx, "a-cap", res.TaskID); again.State != "canceled" || again.Reason != "canceled by @captain"+stoppedNote {
		t.Fatalf("get_task after cancel = %+v", again)
	}
}

// A task still waiting behind another task's turn leaves the line when
// canceled; the running turn goes on and completes.
func TestCancelQueuedTaskLeavesTheRunningTurn(t *testing.T) {
	h, turns, note := newGateHub(t)
	ctx := context.Background()
	first := sendAsync(t, h, "first")
	waitFor(t, "the first turn", func() bool { s, _ := turns.snapshot(); return len(s) == 1 })
	second := sendAsync(t, h, "second")
	waitFor(t, "the second task to queue", func() bool {
		return turns.gate.waiting("sess-a-anton", a2a.TaskID(second.TaskID))
	})

	got, err := h.CancelTask(ctx, "a-cap", second.TaskID)
	if err != nil || got.State != "canceled" || !strings.Contains(got.Reason, "before it started") {
		t.Fatalf("cancel = %+v, %v", got, err)
	}
	if turns.gate.waiting("sess-a-anton", a2a.TaskID(second.TaskID)) {
		t.Fatal("the canceled task is still in the line")
	}
	if _, stopped := turns.snapshot(); len(stopped) != 0 {
		t.Fatalf("a running turn was stopped: %v", stopped)
	}
	close(turns.finish)
	waitFor(t, "the first task to complete", func() bool {
		r, _ := h.GetTask(ctx, "a-cap", first.TaskID)
		return r.State == "completed"
	})
	if r, _ := h.GetTask(ctx, "a-cap", first.TaskID); r.ReplyText != "done "+first.TaskID {
		t.Fatalf("first = %+v", r)
	}
	time.Sleep(20 * time.Millisecond)
	if started, _ := turns.snapshot(); len(started) != 1 {
		t.Fatalf("the canceled task still ran: %v", started)
	}
	for _, d := range func() []string { d, _ := note.snapshot(); return d }() {
		if strings.Contains(d, second.TaskID) {
			t.Fatalf("the canceled task was delivered: %s", d)
		}
	}
}

// Canceled while the teammate is busy with another conversation: that
// turn is left running, the task ends canceled saying so — on disk too —
// and it never runs once the teammate is free.
func TestCancelBehindAnotherConversation(t *testing.T) {
	h, turns, note := newGateHub(t)
	dir := t.TempDir()
	if err := h.Persist(dir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	turns.busy.Store(true)
	res := sendAsync(t, h, "when you have a minute")
	waitFor(t, "the task to queue", func() bool {
		return turns.gate.waiting("sess-a-anton", a2a.TaskID(res.TaskID))
	})

	got, err := h.CancelTask(ctx, "a-cap", res.TaskID)
	want := "canceled by @captain before it started; the teammate's current turn was left running because it belongs to another conversation"
	if err != nil || got.State != "canceled" || got.Reason != want {
		t.Fatalf("cancel = %+v, %v", got, err)
	}
	if list := h.SentFrom("sess-cap"); len(list) != 1 || list[0].State != "canceled" || list[0].Summary != firstLine(want) {
		t.Fatalf("list_tasks = %+v", list)
	}
	turns.busy.Store(false)
	time.Sleep(30 * time.Millisecond)
	if started, stopped := turns.snapshot(); len(started) != 0 || len(stopped) != 0 {
		t.Fatalf("started = %v, stopped = %v", started, stopped)
	}
	if delivered, _ := note.snapshot(); len(delivered) != 0 {
		t.Fatalf("delivered = %v", delivered)
	}

	h2, _, _ := newTestHub(func(Peer, string) string { return "" })
	if err := h2.Persist(dir); err != nil {
		t.Fatal(err)
	}
	if again, err := h2.GetTask(ctx, "a-cap", res.TaskID); err != nil || again.State != "canceled" || again.Reason != want {
		t.Fatalf("after restart = %+v, %v", again, err)
	}
}

// A task canceled before it asked for its turn is refused when it does.
func TestTurnGateRefusesADroppedTask(t *testing.T) {
	g := NewTurnGate(nil)
	if out := g.Stop("s", "t1"); out != TaskStopNotFound {
		t.Fatalf("stop = %v", out)
	}
	if _, err := g.Acquire(context.Background(), "s", "t1"); !errors.Is(err, ErrTaskCanceled) {
		t.Fatalf("acquire = %v", err)
	}
	release, err := g.Acquire(context.Background(), "s", "t2")
	if err != nil {
		t.Fatal(err)
	}
	if out := g.Stop("s", "t2"); out != TaskStopRunning {
		t.Fatalf("stop owner = %v", out)
	}
	release()
	if release, err := g.Acquire(context.Background(), "s", ""); err != nil || release == nil {
		t.Fatalf("ungated turn = %v", err)
	}
}
