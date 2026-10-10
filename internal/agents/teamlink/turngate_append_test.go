package teamlink

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// appendTurns is a teammate on a provider that appends (claude): a
// message typed into the session while a task's turn runs is written into
// that turn and answered when it ends, or lost when the turn is killed.
// Every message passes the gate first (the pool's OnSend).
type appendTurns struct {
	*gateTurns

	amu      sync.Mutex
	inTurn   []string
	answered []string
	killed   int
}

func newAppendHub(t *testing.T) (*Hub, *appendTurns) {
	t.Helper()
	h, _, _ := newTestHub(func(Peer, string) string { return "" })
	turns := &appendTurns{gateTurns: newGateTurns()}
	h.Turns = turns
	return h, turns
}

func (a *appendTurns) Run(ctx context.Context, agent Peer, text string) (string, string, error) {
	session, out, err := a.gateTurns.Run(ctx, agent, text)
	a.amu.Lock()
	if out != "partial work, too late" {
		a.answered = append(a.answered, a.inTurn...)
	}
	a.inTurn = nil
	a.amu.Unlock()
	return session, out, err
}

// typeMessage is a person typing into the teammate's chat.
func (a *appendTurns) typeMessage(text string) {
	a.deliver(WithPersonMessage(context.Background()), text)
}

// deliver is any message into the teammate's chat, passing the gate the
// way the pool's Send does.
func (a *appendTurns) deliver(ctx context.Context, text string) {
	done, err := a.gate.NoteMessage(ctx, "sess-a-anton", "user")
	if err != nil {
		return
	}
	a.amu.Lock()
	a.inTurn = append(a.inTurn, text)
	a.amu.Unlock()
	done(true)
}

func (a *appendTurns) StopTask(_ context.Context, _ Peer, sessionID string, id a2a.TaskID, _ string) (TaskStop, error) {
	return a.gate.StopWith(sessionID, id, func() (TaskStop, error) {
		a.mu.Lock()
		a.stopped = append(a.stopped, id)
		close(a.stops[id])
		a.mu.Unlock()
		a.amu.Lock()
		a.killed++
		a.amu.Unlock()
		return TaskStopRunning, nil
	})
}

func (a *appendTurns) result() (answered []string, killed int) {
	a.amu.Lock()
	defer a.amu.Unlock()
	return append([]string(nil), a.answered...), a.killed
}

// On an appending provider, a message typed into the teammate's chat while
// a task's turn runs keeps that turn alive on cancel: the task is canceled,
// the turn is not killed, and the message is still answered.
func TestCancelSparesTurnAMessageReached(t *testing.T) {
	h, turns := newAppendHub(t)
	ctx := context.Background()
	res := sendAsync(t, h, "long scan")
	waitFor(t, "the task's turn", func() bool { s, _ := turns.snapshot(); return len(s) == 1 })

	turns.typeMessage("are you still there?")
	got, err := h.CancelTask(ctx, "a-cap", res.TaskID)
	want := "canceled by @captain; the teammate's turn was left running because a message from another conversation reached it while it ran"
	if err != nil || got.State != "canceled" || got.Reason != want {
		t.Fatalf("cancel = %+v, %v", got, err)
	}
	if _, killed := turns.result(); killed != 0 {
		t.Fatal("the turn was killed with the typed message in it")
	}
	close(turns.finish)
	waitFor(t, "the message answered", func() bool { a, _ := turns.result(); return len(a) == 1 })
	if again, _ := h.GetTask(ctx, "a-cap", res.TaskID); again.State != "canceled" {
		t.Fatalf("task after the turn ended = %+v", again)
	}
	// The mark ends with the turn.
	waitFor(t, "the gate let go", func() bool {
		turns.gate.mu.Lock()
		defer turns.gate.mu.Unlock()
		return turns.gate.sessions["sess-a-anton"] == nil
	})
}

// With nothing typed into it, the task's turn is still killed on cancel.
func TestCancelKillsTurnNoMessageReached(t *testing.T) {
	h, turns := newAppendHub(t)
	ctx := context.Background()
	res := sendAsync(t, h, "long scan")
	waitFor(t, "the task's turn", func() bool { s, _ := turns.snapshot(); return len(s) == 1 })

	got, err := h.CancelTask(ctx, "a-cap", res.TaskID)
	if err != nil || got.State != "canceled" || got.Reason != "canceled by @captain"+stoppedNote {
		t.Fatalf("cancel = %+v, %v", got, err)
	}
	if _, killed := turns.result(); killed != 1 {
		t.Fatalf("killed = %d, want 1", killed)
	}
}

// The mark is the running turn's: the task's own message does not set it,
// a turn that ended takes it along, and a message that arrives while the
// turn is being stopped waits for the stop instead of slipping into it.
func TestTurnGateMessageMark(t *testing.T) {
	g := NewTurnGate(nil)
	ctx := context.Background()
	release, err := g.Acquire(ctx, "s1", "t1")
	if err != nil {
		t.Fatal(err)
	}
	note := func(ctx context.Context, role string) {
		t.Helper()
		done, err := g.NoteMessage(ctx, "s1", role)
		if err != nil {
			t.Fatal(err)
		}
		done(true)
	}
	note(withTaskID(ctx, "t1"), "user")
	note(ctx, "user")
	note(WithPersonMessage(ctx), "system")
	if got := g.Stop("s1", "t1"); got != TaskStopRunning {
		t.Fatalf("own or untagged message: stop = %v, want running", got)
	}
	refused, err := g.NoteMessage(WithPersonMessage(ctx), "s1", "user")
	if err != nil {
		t.Fatal(err)
	}
	refused(false)
	if got := g.Stop("s1", "t1"); got != TaskStopRunning {
		t.Fatalf("refused message: stop = %v, want running", got)
	}
	note(WithPersonMessage(ctx), "user")
	if got := g.Stop("s1", "t1"); got != TaskStopShared {
		t.Fatalf("typed message: stop = %v, want shared", got)
	}
	release()

	release, err = g.Acquire(ctx, "s1", "t2")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if got := g.Stop("s1", "t2"); got != TaskStopRunning {
		t.Fatalf("mark outlived its turn: stop = %v", got)
	}

	killing, kill := make(chan struct{}), make(chan struct{})
	stopped := make(chan TaskStop, 1)
	go func() {
		out, _ := g.StopWith("s1", "t2", func() (TaskStop, error) {
			close(killing)
			<-kill
			return TaskStopRunning, nil
		})
		stopped <- out
	}()
	<-killing
	noted := make(chan struct{})
	go func() {
		done, _ := g.NoteMessage(WithPersonMessage(ctx), "s1", "user")
		done(true)
		close(noted)
	}()
	select {
	case <-noted:
		t.Fatal("a message slipped in while the turn was being stopped")
	case <-time.After(30 * time.Millisecond):
	}
	close(kill)
	<-noted
	if out := <-stopped; out != TaskStopRunning {
		t.Fatalf("stop = %v", out)
	}
}

// A sub-agent's result or a teammate's reply arriving while a task's turn
// runs is most often that task's own work coming back: it does not mark
// the turn, so a cancel still stops it.
func TestCancelKillsTurnTheTaskOwnResultReached(t *testing.T) {
	h, turns := newAppendHub(t)
	ctx := context.Background()
	res := sendAsync(t, h, "long scan")
	waitFor(t, "the task's turn", func() bool { s, _ := turns.snapshot(); return len(s) == 1 })

	turns.deliver(ctx, "Reply from @scout [task x, completed]: done")
	got, err := h.CancelTask(ctx, "a-cap", res.TaskID)
	if err != nil || got.State != "canceled" || got.Reason != "canceled by @captain"+stoppedNote {
		t.Fatalf("cancel = %+v, %v", got, err)
	}
	if _, killed := turns.result(); killed != 1 {
		t.Fatalf("killed = %d, want 1", killed)
	}
}

// A person's message still on its way when the task takes the session
// would land in the task's turn unmarked: the task waits for it, and a
// cancel while it is on its way leaves the turn running.
func TestTurnGateMessageOnItsWay(t *testing.T) {
	g := NewTurnGate(nil)
	g.poll = time.Millisecond
	ctx := context.Background()
	done, err := g.NoteMessage(WithPersonMessage(ctx), "s1", "user")
	if err != nil {
		t.Fatal(err)
	}
	acquired := make(chan func(), 1)
	go func() {
		release, err := g.Acquire(ctx, "s1", "t1")
		if err != nil {
			t.Error(err)
		}
		acquired <- release
	}()
	select {
	case <-acquired:
		t.Fatal("the task took the session with a message on its way")
	case <-time.After(30 * time.Millisecond):
	}
	done(true)
	release := <-acquired
	defer release()

	// On its way while the turn runs: the cancel spares the turn.
	done, err = g.NoteMessage(WithPersonMessage(ctx), "s1", "user")
	if err != nil {
		t.Fatal(err)
	}
	killed := false
	out, _ := g.StopWith("s1", "t1", func() (TaskStop, error) { killed = true; return TaskStopRunning, nil })
	if out != TaskStopShared || killed {
		t.Fatalf("stop = %v, killed = %v; want shared, not killed", out, killed)
	}
	done(false)
}

// A message held behind a stop gives up when its sender's ctx ends, and
// goes through once the stop takes longer than the cap.
func TestTurnGateHoldEnds(t *testing.T) {
	g := NewTurnGate(nil)
	ctx := context.Background()
	release, err := g.Acquire(ctx, "s1", "t1")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	killing, kill := make(chan struct{}), make(chan struct{})
	go func() {
		_, _ = g.StopWith("s1", "t1", func() (TaskStop, error) {
			close(killing)
			<-kill
			return TaskStopRunning, nil
		})
	}()
	<-killing
	defer close(kill)

	gone, cancel := context.WithCancel(WithPersonMessage(ctx))
	cancel()
	if _, err := g.NoteMessage(gone, "s1", "user"); err != context.Canceled {
		t.Fatalf("canceled sender: err = %v", err)
	}

	g.mu.Lock()
	g.stopWait = 20 * time.Millisecond
	g.mu.Unlock()
	done, err := g.NoteMessage(WithPersonMessage(ctx), "s1", "user")
	if err != nil {
		t.Fatalf("held past the cap: err = %v", err)
	}
	done(true)
}
