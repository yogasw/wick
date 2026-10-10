package teamlink

import (
	"context"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
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
	if len(list) != 1 || list[0].State != "failed" || !list[0].Interrupted {
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

// waitFor polls cond until it holds, failing the test after 2s.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); !cond(); time.Sleep(2 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}
