package teamlink

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// waitDelivered waits until note has n deliveries and returns them.
func waitDelivered(t *testing.T, note *fakeNotify, n int) []string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		delivered, _ := note.snapshot()
		if len(delivered) >= n {
			return delivered
		}
		if time.Now().After(deadline) {
			t.Fatalf("deliveries = %v, want %d", delivered, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// The default wait is short: a slow teammate hands back working.
func TestQuickWaitReturnsWorking(t *testing.T) {
	h, turns, note := newTestHub(func(Peer, string) string { return "later" })
	h.maxWait = 30 * time.Millisecond
	turns.gate = make(chan struct{})
	start := time.Now()
	res, err := h.Send(context.Background(), SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "slow"})
	if err != nil || res.State != "working" || time.Since(start) > 2*time.Second {
		t.Fatalf("result = %+v, %v after %s", res, err, time.Since(start))
	}
	close(turns.gate)
	if got := waitDelivered(t, note, 1); !strings.Contains(got[0], "later") {
		t.Fatalf("delivered = %v", got)
	}
}

// Messages sent one after another while the first is still working do not
// each wait: the conversation is fanning out.
func TestFanOutDoesNotWait(t *testing.T) {
	h, turns, note := newTestHub(func(p Peer, _ string) string { return "from " + p.Handle })
	h.maxWait = 500 * time.Millisecond
	turns.gate = make(chan struct{})
	ctx := context.Background()
	if res, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "a"}); err != nil || res.State != "working" {
		t.Fatalf("first = %+v, %v", res, err)
	}
	start := time.Now()
	res, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "vera", Text: "b"})
	if err != nil || res.State != "working" || time.Since(start) > 300*time.Millisecond {
		t.Fatalf("second = %+v, %v after %s", res, err, time.Since(start))
	}
	close(turns.gate)
	waitDelivered(t, note, 2)
}

// A tool call cut off while it waits still gets the reply delivered.
func TestCanceledCallStillDelivers(t *testing.T) {
	h, turns, note := newTestHub(func(Peer, string) string { return "answer after the cut" })
	turns.gate = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "slow"})
		done <- err
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	close(turns.gate)
	got := waitDelivered(t, note, 1)
	if !strings.HasPrefix(got[0], "sess-cap: Reply from Anton (@anton)") || !strings.HasSuffix(got[0], "answer after the cut") {
		t.Fatalf("delivered = %v", got)
	}
	time.Sleep(20 * time.Millisecond)
	if again, _ := note.snapshot(); len(again) != 1 {
		t.Fatalf("delivered twice: %v", again)
	}
}

// A teammate that asks back ends input_required; the answer goes to the
// same task with task_id and completes it.
func TestInputRequiredAnsweredWithTaskID(t *testing.T) {
	h, turns, _ := newTestHub(func(_ Peer, text string) string {
		if strings.HasSuffix(text, "prod") {
			return "deployed to prod"
		}
		return InputRequiredToken + " which environment?"
	})
	ctx := context.Background()
	q, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "deploy it"})
	if err != nil || q.State != "input_required" || q.ReplyText != "which environment?" || !strings.Contains(q.Note, q.TaskID) {
		t.Fatalf("question = %+v, %v", q, err)
	}
	a, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", TaskID: q.TaskID, Text: "prod"})
	if err != nil || a.State != "completed" || a.TaskID != q.TaskID || a.ContextID != q.ContextID || a.ReplyText != "deployed to prod" {
		t.Fatalf("answer = %+v, %v", a, err)
	}
	if len(turns.seen) != 2 || !strings.HasPrefix(turns.seen[1], "anton <- ") {
		t.Fatalf("turns = %v", turns.seen)
	}
	// Settled now: a second answer is refused.
	if _, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", TaskID: q.TaskID, Text: "again"}); !errors.Is(err, ErrTaskNotWaiting) {
		t.Fatalf("answer to a completed task: %v", err)
	}
}

// task_id must match its context_id and teammate, and be the caller's.
func TestTaskIDChecks(t *testing.T) {
	h, _, _ := newTestHub(func(Peer, string) string { return InputRequiredToken + " which one?" })
	ctx := context.Background()
	q, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "fix it"})
	if err != nil || q.State != "input_required" {
		t.Fatalf("question = %+v, %v", q, err)
	}
	for name, tc := range map[string]struct {
		in   SendInput
		want error
	}{
		"other context": {SendInput{CallerAgentID: "a-cap", TaskID: q.TaskID, ContextID: "ctx-other", Text: "x"}, ErrTaskContext},
		"other agent":   {SendInput{CallerAgentID: "a-cap", TaskID: q.TaskID, To: "vera", Text: "x"}, ErrTaskOtherAgent},
		"other caller":  {SendInput{CallerAgentID: "a-vera", TaskID: q.TaskID, To: "anton", Text: "x"}, ErrUnknownTask},
		"unknown task":  {SendInput{CallerAgentID: "a-cap", TaskID: "nope", Text: "x"}, ErrUnknownTask},
	} {
		if _, err := h.Send(ctx, tc.in); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
	// The matching context is fine.
	if _, err := h.Send(ctx, SendInput{CallerAgentID: "a-cap", TaskID: q.TaskID, ContextID: q.ContextID, To: "@anton", Text: "the API"}); err != nil {
		t.Fatalf("matching answer: %v", err)
	}
}

// stopTurns is fakeTurns that knows each agent's session and can stop a
// turn: stopping releases it.
type stopTurns struct {
	*fakeTurns
	mu      sync.Mutex
	stopped []string
	once    sync.Once
}

func (s *stopTurns) MainSession(_ context.Context, agent Peer) string { return "sess-" + agent.ID }

func (s *stopTurns) StopTask(_ context.Context, _ Peer, sessionID string, _ a2a.TaskID, by string) (TaskStop, error) {
	s.mu.Lock()
	s.stopped = append(s.stopped, sessionID+" by "+by)
	s.mu.Unlock()
	s.once.Do(func() { close(s.gate) })
	return TaskStopRunning, nil
}

func TestListAndCancelTask(t *testing.T) {
	h, turns, note := newTestHub(func(Peer, string) string { return "too late" })
	turns.gate = make(chan struct{})
	st := &stopTurns{fakeTurns: turns}
	h.Turns = st
	ctx := context.Background()
	res, err := h.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "long scan\nwith detail", Wait: -1})
	if err != nil || res.State != "working" {
		t.Fatalf("send = %+v, %v", res, err)
	}
	// Cancel once the teammate's turn is running.
	for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		turns.mu.Lock()
		n := len(turns.seen)
		turns.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("turn never started")
		}
	}
	list := h.SentFrom("sess-cap")
	if len(list) != 1 || list[0].TaskID != res.TaskID || list[0].State != "working" || list[0].Title != "long scan" || list[0].Age == "" {
		t.Fatalf("list = %+v", list)
	}
	if _, err := h.CancelTask(ctx, "a-vera", res.TaskID); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("another agent canceled: %v", err)
	}
	got, err := h.CancelTask(ctx, "a-cap", res.TaskID)
	if err != nil || got.State != "canceled" || got.Reason != "canceled by @captain"+stoppedNote {
		t.Fatalf("cancel = %+v, %v", got, err)
	}
	st.mu.Lock()
	stopped := strings.Join(st.stopped, ",")
	st.mu.Unlock()
	if stopped != "sess-a-anton by @captain" {
		t.Fatalf("stopped = %q", stopped)
	}
	time.Sleep(50 * time.Millisecond)
	if delivered, _ := note.snapshot(); len(delivered) != 0 {
		t.Fatalf("a canceled task still delivered: %v", delivered)
	}
	if again, err := h.GetTask(ctx, "a-cap", res.TaskID); err != nil || again.State != "canceled" {
		t.Fatalf("get_task after cancel = %+v, %v", again, err)
	}
	if list := h.SentFrom("sess-cap"); list[0].State != "canceled" || list[0].Summary != "canceled by @captain"+stoppedNote {
		t.Fatalf("list after cancel = %+v", list)
	}
	if _, err := h.CancelTask(ctx, "a-cap", res.TaskID); !errors.Is(err, ErrTaskSettled) {
		t.Fatalf("second cancel: %v", err)
	}
}

// A follow-up that repeats the reply the caller already has (a Slack
// remote re-posting its answer with other markup) is not delivered again.
func TestFollowUpDedupe(t *testing.T) {
	h, _, note := newTestHub(func(Peer, string) string { return "**1.** Pick the *specialist* by role" })
	if res, err := h.Send(context.Background(), SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "how?"}); err != nil || res.State != "completed" {
		t.Fatalf("send = %+v, %v", res, err)
	}
	ctx := context.Background()
	if !h.FollowUp(ctx, "sess-a-anton", "1. Pick the specialist by role") {
		t.Fatal("duplicate follow-up not handled")
	}
	if delivered, _ := note.snapshot(); len(delivered) != 0 {
		t.Fatalf("duplicate delivered: %v", delivered)
	}
	if !h.FollowUp(ctx, "sess-a-anton", "2. Then ask it") {
		t.Fatal("new follow-up not delivered")
	}
	h.FollowUp(ctx, "sess-a-anton", "2. Then  ask it")
	if delivered, _ := note.snapshot(); len(delivered) != 1 || !strings.HasSuffix(delivered[0], "2. Then ask it") {
		t.Fatalf("delivered = %v", delivered)
	}
}

// A new Hub over the same folder still knows the tasks: get_task,
// list_tasks, follow-ups and the answer to a question survive a restart.
func TestTasksPersistAcrossHubs(t *testing.T) {
	dir := t.TempDir()
	h1, _, _ := newTestHub(func(_ Peer, text string) string {
		if strings.Contains(text, "deploy") {
			return InputRequiredToken + " which environment?"
		}
		return "no 401s"
	})
	if err := h1.Persist(dir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	done, err := h1.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "any 401s?"})
	if err != nil || done.State != "completed" {
		t.Fatalf("send = %+v, %v", done, err)
	}
	q, err := h1.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "vera", Text: "deploy it"})
	if err != nil || q.State != "input_required" {
		t.Fatalf("question = %+v, %v", q, err)
	}

	h2, _, note := newTestHub(func(Peer, string) string { return "deployed to prod" })
	if err := h2.Persist(dir); err != nil {
		t.Fatal(err)
	}
	got, err := h2.GetTask(ctx, "a-cap", done.TaskID)
	if err != nil || got.State != "completed" || got.ReplyText != "no 401s" || got.ContextID != done.ContextID {
		t.Fatalf("get_task after restart = %+v, %v", got, err)
	}
	if list := h2.SentFrom("sess-cap"); len(list) != 2 {
		t.Fatalf("list after restart = %+v", list)
	}
	if !h2.FollowUp(ctx, "sess-a-anton", "also no 500s") {
		t.Fatal("follow-up after restart not delivered")
	}
	if delivered, _ := note.snapshot(); len(delivered) != 1 || !strings.HasPrefix(delivered[0], "sess-cap: Follow-up from Anton (@anton) [task "+done.TaskID) {
		t.Fatalf("delivered = %v", delivered)
	}
	// The task store forgot the question with the restart: the answer
	// goes as a new task of the same exchange.
	a, err := h2.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", TaskID: q.TaskID, Text: "prod"})
	if err != nil || a.State != "completed" || a.ContextID != q.ContextID || a.ReplyText != "deployed to prod" || !strings.Contains(a.Note, "restarted") {
		t.Fatalf("answer after restart = %+v, %v", a, err)
	}

	// Files older than TaskKeep are dropped on load.
	h3, _, _ := newTestHub(func(Peer, string) string { return "" })
	h3.now = func() time.Time { return time.Now().Add(TaskKeep + time.Hour) }
	if err := h3.Persist(dir); err != nil {
		t.Fatal(err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*.json")); len(left) != 0 {
		t.Fatalf("old files kept: %v", left)
	}
	if _, err := h3.GetTask(ctx, "a-cap", done.TaskID); !errors.Is(err, ErrUnknownTask) {
		t.Fatalf("expired task still read: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}

// Every A2A state maps to one of the words the tool reports.
func TestStateNames(t *testing.T) {
	for in, want := range map[string]string{
		"": "working", "TASK_STATE_SUBMITTED": "working", "TASK_STATE_WORKING": "working",
		"TASK_STATE_INPUT_REQUIRED": "input_required", "TASK_STATE_COMPLETED": "completed",
		"TASK_STATE_FAILED": "failed", "TASK_STATE_CANCELED": "canceled", "TASK_STATE_REJECTED": "rejected",
		"TASK_STATE_AUTH_REQUIRED": "rejected",
	} {
		if got := stateName(a2a.TaskState(in)); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}
