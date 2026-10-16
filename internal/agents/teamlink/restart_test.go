package teamlink

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// sendWorking sends a task from a fresh Hub persisted in dir whose
// teammate never finishes (until the test ends), returning its id once the
// record on disk reads working.
func sendWorking(t *testing.T, dir string) (string, chan struct{}) {
	t.Helper()
	h1, turns, _ := newTestHub(func(Peer, string) string { return "scan done" })
	turns.gate = make(chan struct{})
	if err := h1.Persist(dir); err != nil {
		t.Fatal(err)
	}
	res, err := h1.Send(context.Background(), SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "long scan", Wait: -1})
	if err != nil || res.State != "working" {
		t.Fatalf("send = %+v, %v", res, err)
	}
	waitFor(t, "the working record", func() bool {
		rec, ok := readRecord(filepath.Join(dir, res.TaskID+".json"))
		return ok && rec.WaiterGone && !rec.Finished
	})
	return res.TaskID, turns.gate
}

// P34-6: a task that was working when wick restarted is settled failed
// as interrupted (memory, disk, the list and the sender), not left
// "working" for TaskKeep; sending it again works.
func TestRestartSettlesAWorkingTaskAsInterrupted(t *testing.T) {
	dir := t.TempDir()
	id, gate := sendWorking(t, dir)
	defer close(gate)

	h2, _, note := newTestHub(func(Peer, string) string { return "scanned again" })
	h2.orphanPoll = 5 * time.Millisecond
	if err := h2.Persist(dir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	waitFor(t, "the task to settle", func() bool {
		got, err := h2.GetTask(ctx, "a-cap", id)
		return err == nil && got.State == "failed"
	})
	got, _ := h2.GetTask(ctx, "a-cap", id)
	if got.Reason != interruptedReason {
		t.Fatalf("reason = %q", got.Reason)
	}
	list := h2.SentFrom("sess-cap")
	if len(list) != 1 || list[0].State != "failed" || !list[0].Interrupted || list[0].NeedsYou {
		t.Fatalf("list = %+v", list)
	}
	delivered := waitDelivered(t, note, 1)
	if len(delivered) != 1 || !strings.HasPrefix(delivered[0], "sess-cap: Reply from Anton (@anton) [task "+id+", failed]") ||
		!strings.Contains(delivered[0], "Send it again") {
		t.Fatalf("delivered = %v", delivered)
	}
	if rec, ok := readRecord(filepath.Join(dir, id+".json")); !ok || !rec.Finished || !rec.Interrupted || rec.State != "TASK_STATE_FAILED" {
		t.Fatalf("record = %+v", rec)
	}

	// Settled for good: another restart reads it failed and tells nobody.
	h3, _, note3 := newTestHub(func(Peer, string) string { return "" })
	h3.orphanPoll = 5 * time.Millisecond
	if err := h3.Persist(dir); err != nil {
		t.Fatal(err)
	}
	if list := h3.SentFrom("sess-cap"); len(list) != 1 || list[0].State != "failed" || !list[0].Interrupted {
		t.Fatalf("list after another restart = %+v", list)
	}
	time.Sleep(30 * time.Millisecond)
	if d, _ := note3.snapshot(); len(d) != 0 {
		t.Fatalf("delivered again = %v", d)
	}

	// Sending it again is a new task that runs.
	again, err := h2.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "anton", Text: "long scan"})
	if err != nil || again.State != "completed" || again.ReplyText != "scanned again" {
		t.Fatalf("resend = %+v, %v", again, err)
	}
}

// A draining predecessor may still finish the task: it is left working
// until that process lets go, and what it finished is kept, not
// overwritten as interrupted.
func TestRestartLeavesATaskTheDrainingProcessFinishes(t *testing.T) {
	dir := t.TempDir()
	id, gate := sendWorking(t, dir)

	h2, _, note := newTestHub(func(Peer, string) string { return "" })
	var draining atomic.Bool
	draining.Store(true)
	h2.PredecessorBusy = func(string) bool { return draining.Load() }
	if err := h2.Persist(dir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if left := h2.settleOrphans(ctx); left != 1 {
		t.Fatalf("left = %d, want 1", left)
	}
	if list := h2.SentFrom("sess-cap"); len(list) != 1 || list[0].State != "working" || list[0].Interrupted {
		t.Fatalf("list while draining = %+v", list)
	}

	// The predecessor finishes the task, then exits.
	close(gate)
	waitFor(t, "the predecessor's reply on disk", func() bool {
		rec, ok := readRecord(filepath.Join(dir, id+".json"))
		return ok && rec.Finished
	})
	draining.Store(false)
	if list := h2.SentFrom("sess-cap"); len(list) != 1 || list[0].State != "completed" || list[0].Interrupted {
		t.Fatalf("list after the predecessor finished = %+v", list)
	}
	if left := h2.settleOrphans(ctx); left != 0 {
		t.Fatalf("left = %d", left)
	}
	if got, err := h2.GetTask(ctx, "a-cap", id); err != nil || got.State != "completed" || got.ReplyText != "scan done" {
		t.Fatalf("get_task = %+v, %v", got, err)
	}
	if d, _ := note.snapshot(); len(d) != 0 {
		t.Fatalf("the new process delivered = %v", d)
	}
}

// A task answered in this process after a restart is not a leftover any
// more, even while its new turn runs.
func TestAnsweredTaskIsNotAnOrphan(t *testing.T) {
	h, _, _ := newTestHub(func(Peer, string) string { return "" })
	h.mu.Lock()
	ref := refFromRecord(taskRecord{TaskID: "t1", AgentID: "a-anton", CallerAgentID: "a-cap", CallerSession: "sess-cap",
		Finished: true, State: "TASK_STATE_INPUT_REQUIRED", Started: time.Now(), Touched: time.Now()})
	h.tasks["t1"] = ref
	if h.orphanLocked("t1", ref) {
		t.Fatal("a question waiting for its answer is no leftover")
	}
	h.mu.Unlock()
	if _, _, _, err := h.answering("a-cap", SendInput{TaskID: "t1", Text: "prod"}); err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if ref.finished || !ref.claimed || h.orphanLocked("t1", ref) {
		t.Fatalf("answered task reads as a leftover: %+v", ref)
	}
}

// S3: the task store lost the question with a restart, so the user's
// answer goes as a new task of the exchange; the old task is settled
// canceled as superseded, its claim cleared, and the teammate gets the
// answer exactly once.
func TestUserAnswerToALostTaskSupersedesIt(t *testing.T) {
	dir := t.TempDir()
	h1, _, _ := newTestHub(func(Peer, string) string { return InputRequiredToken + " which environment?" })
	if err := h1.Persist(dir); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	q, err := h1.Send(ctx, SendInput{CallerSession: "sess-cap", CallerAgentID: "a-cap", To: "vera", Text: "deploy it"})
	if err != nil || q.State != "input_required" {
		t.Fatalf("question = %+v, %v", q, err)
	}

	h2, turns, _ := newTestHub(func(Peer, string) string { return "deployed to prod" })
	if err := h2.Persist(dir); err != nil {
		t.Fatal(err)
	}
	if list := h2.SentFrom("sess-cap"); len(list) != 1 || !list[0].NeedsYou {
		t.Fatalf("list before the answer = %+v", list)
	}
	a, err := h2.AnswerFromUser(ctx, "sess-cap", q.TaskID, "prod")
	if err != nil || a.TaskID == q.TaskID || a.ContextID != q.ContextID {
		t.Fatalf("answer = %+v, %v", a, err)
	}
	waitFor(t, "the new task to finish", func() bool {
		got, err := h2.GetTask(ctx, "a-cap", a.TaskID)
		return err == nil && got.State == "completed"
	})

	old, err := h2.GetTask(ctx, "a-cap", q.TaskID)
	if err != nil || old.State != "canceled" || !strings.Contains(old.Reason, "superseded by task "+a.TaskID) {
		t.Fatalf("old task = %+v, %v", old, err)
	}
	h2.mu.Lock()
	ref := h2.tasks[a2a.TaskID(q.TaskID)]
	claimed, userAnswer := ref.claimed, ref.userAnswer
	h2.mu.Unlock()
	if claimed || userAnswer {
		t.Fatalf("old task still claimed: claimed=%v userAnswer=%v", claimed, userAnswer)
	}
	list := h2.SentFrom("sess-cap")
	states := map[string]string{}
	for _, v := range list {
		states[v.TaskID] = v.State
		if v.NeedsYou {
			t.Fatalf("still needs the user: %+v", v)
		}
	}
	if len(list) != 2 || states[q.TaskID] != "canceled" || states[a.TaskID] != "completed" {
		t.Fatalf("list = %+v", list)
	}
	turns.mu.Lock()
	seen := append([]string(nil), turns.seen...)
	turns.mu.Unlock()
	if len(seen) != 1 || !strings.Contains(seen[0], "prod") {
		t.Fatalf("teammate turns = %v", seen)
	}
	// A second answer to the old task is refused, not sent again.
	if _, err := h2.AnswerFromUser(ctx, "sess-cap", q.TaskID, "staging"); err == nil {
		t.Fatal("the superseded task took a second answer")
	}
	if rec, ok := readRecord(filepath.Join(dir, q.TaskID+".json")); !ok || !rec.Canceled || !rec.Finished {
		t.Fatalf("old record = %+v", rec)
	}
}
