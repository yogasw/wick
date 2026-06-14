package setup

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// blockingExec gates Execute on a release channel so a test can hold a
// run "in flight" while it does other things to the router.
type blockingExec struct {
	entered chan struct{} // closed-on-first-enter signal
	release chan struct{} // test closes this to let Execute return
	once    bool
}

func (e *blockingExec) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	if !e.once {
		e.once = true
		close(e.entered)
	}
	select {
	case <-e.release:
	case <-ctx.Done():
		return workflow.NodeOutput{}, ctx.Err()
	}
	return workflow.NodeOutput{Result: "done"}, nil
}

// TestPublishNewWorkflow_DoesNotInterruptInFlightRun is the side-effect
// guard the user asked about: while workflow A has a run mid-execution,
// publishing a brand-new workflow B (and re-registering A) must NOT stop,
// cancel, or restart A's running run. Worker isolation is per-id, and the
// baseCtx fix keeps every worker bound to the server ctx, so a second
// Register can't tear down the first worker.
func TestPublishNewWorkflow_DoesNotInterruptInFlightRun(t *testing.T) {
	m := newMgr(t)
	require.NoError(t, m.Start(context.Background()))

	// Swap a blocking executor in for shell nodes on THIS manager's engine
	// only (each test gets its own Manager, so this is isolated).
	be := &blockingExec{entered: make(chan struct{}), release: make(chan struct{})}
	m.Engine.Register(workflow.NodeShell, be)

	wfA := workflow.Workflow{
		ID:       "iso-a",
		Version:  1,
		Name:     "A",
		Enabled:  true,
		Triggers: []workflow.Trigger{{Type: workflow.TriggerManual}},
		Graph: workflow.Graph{
			Entry: "block",
			Nodes: []workflow.Node{
				{ID: "block", Type: workflow.NodeShell, Command: []string{"noop"}},
				{ID: "done", Type: workflow.NodeEnd, Result: "ok"},
			},
			Edges: []workflow.Edge{{From: "block", To: "done"}},
		},
	}
	require.NoError(t, m.Service.Create(wfA.ID, wfA))
	require.NoError(t, HotReload(context.Background(), m.Service, m.Router, m.Cron, nil, wfA.ID))

	// Kick A via the async router path (real worker, not direct engine).
	done, err := m.Router.RunNowWithDone(context.Background(), wfA.ID, nil,
		workflow.Event{Type: string(workflow.TriggerManual), At: time.Now()})
	require.NoError(t, err)

	// Wait until A is actually inside the blocking node.
	select {
	case <-be.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("workflow A never entered its node")
	}

	// ── While A is mid-run: publish a NEW workflow B + re-register A.
	wfB := workflow.Workflow{
		ID:       "iso-b",
		Version:  1,
		Name:     "B",
		Enabled:  true,
		Triggers: []workflow.Trigger{{Type: workflow.TriggerManual}},
		Graph: workflow.Graph{
			Entry: "end",
			Nodes: []workflow.Node{{ID: "end", Type: workflow.NodeEnd, Result: "ok"}},
		},
	}
	require.NoError(t, m.Service.Create(wfB.ID, wfB))
	require.NoError(t, HotReload(context.Background(), m.Service, m.Router, m.Cron, nil, wfB.ID))
	// Re-register A too (mirrors a re-publish of A while it runs).
	require.NoError(t, HotReload(context.Background(), m.Service, m.Router, m.Cron, nil, wfA.ID))

	// A's worker must still be alive — registering B/A again did not kill it.
	require.True(t, m.Router.WorkerAlive(wfA.ID), "A worker died after publishing B / re-registering A")

	// Release A. It must finish cleanly — not cancelled, not failed.
	close(be.release)
	select {
	case res := <-done:
		require.NoError(t, res.Err, "in-flight run A failed after a concurrent publish")
		require.Equal(t, workflow.StatusSuccess, res.State.Status)
	case <-time.After(2 * time.Second):
		t.Fatal("workflow A never completed after release")
	}
}
