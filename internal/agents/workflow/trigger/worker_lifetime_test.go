package trigger

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// TestRegister_WorkerSurvivesCallerCtxCancel is the regression guard for
// the silent-run bug: publishing a workflow from an HTTP handler passed
// the request ctx to Register, so the worker died the instant the response
// flushed — the queue lingered and runs enqueued but never drained until a
// restart re-registered under the server ctx.
//
// With SetBaseCtx pinning the server-lifetime ctx, a Register call whose
// caller ctx is ALREADY cancelled must still produce a live worker.
func TestRegister_WorkerSurvivesCallerCtxCancel(t *testing.T) {
	r := newTestRouter()
	r.SetBaseCtx(context.Background())

	w := workflow.Workflow{
		ID:      "wf-lifetime",
		Enabled: true,
		Triggers: []workflow.Trigger{{Type: workflow.TriggerManual, EntryNode: "n"}},
	}

	// Simulate the HTTP-handler path: a request ctx that cancels right away.
	reqCtx, cancel := context.WithCancel(context.Background())
	cancel()
	r.Register(reqCtx, w)

	// Give the (wrongly-bound) worker a chance to die if the bug regressed.
	time.Sleep(20 * time.Millisecond)

	r.mu.RLock()
	h := r.workers[w.ID]
	r.mu.RUnlock()
	if h == nil {
		t.Fatal("no worker handle registered")
	}
	if !h.alive.Load() {
		t.Fatal("worker died after caller ctx cancelled — baseCtx not honoured (regression)")
	}
}

// TestStop_MarksWorkerDead verifies the alive flag flips on shutdown so
// Dispatch can report a workerless queue truthfully.
func TestStop_MarksWorkerDead(t *testing.T) {
	r := newTestRouter()
	r.SetBaseCtx(context.Background())
	w := workflow.Workflow{
		ID:       "wf-stop",
		Enabled:  true,
		Triggers: []workflow.Trigger{{Type: workflow.TriggerManual, EntryNode: "n"}},
	}
	r.Register(context.Background(), w)

	r.mu.RLock()
	h := r.workers[w.ID]
	r.mu.RUnlock()
	if h == nil || !h.alive.Load() {
		t.Fatal("worker not alive after Register")
	}

	r.Unregister(w.ID)
	// Worker exits asynchronously after Dequeue unblocks.
	deadline := time.Now().Add(time.Second)
	for h.alive.Load() {
		if time.Now().After(deadline) {
			t.Fatal("worker still marked alive after Unregister")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
