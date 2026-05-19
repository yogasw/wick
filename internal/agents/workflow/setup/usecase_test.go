// Package setup tests cover end-to-end Manager flows for the four
// canonical workflow shapes users hit:
//
//   1. Manual single trigger → shell → end (most common smoke test)
//   2. Multi-trigger (cron + manual + webhook) — verify each entry
//      and that router.RunNow bypasses Enabled check
//   3. Template render through nodes — Event.X / Node.X / Env.X
//   4. Connector node — wraps pkg/connector module + dispatches
//
// Each test bootstraps a fresh Manager in t.TempDir() and asserts on
// events.jsonl + run state. No HTTP, no auth — direct engine drive.
package setup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/connector"
	"github.com/yogasw/wick/internal/agents/workflow/parse"
	pkgconnector "github.com/yogasw/wick/pkg/connector"
)

// newMgr builds a fresh Manager rooted at a temp BaseDir. Callers
// register providers/connectors via WithProvider before Start.
func newMgr(t *testing.T) *Manager {
	t.Helper()
	layout := config.Layout{BaseDir: t.TempDir()}
	require.NoError(t, layout.EnsureLayout())
	return New(layout)
}

// runWorkflow saves a workflow to disk, registers it with the
// router, and synchronously waits for one run completion. Returns
// the run_id so tests can inspect state + events.
func runWorkflow(t *testing.T, m *Manager, w workflow.Workflow, evt workflow.Event) string {
	t.Helper()
	require.NoError(t, m.Service.Create(w.ID, w, nil))
	require.NoError(t, HotReload(context.Background(), m.Service, m.Router, m.Cron, nil, w.ID))

	// Run via engine direct so we get a deterministic synchronous
	// result — Router.RunNow + worker goroutine is async.
	loaded, err := m.Service.Load(w.ID)
	require.NoError(t, err)
	st, err := m.Engine.Run(context.Background(), loaded, evt)
	require.NoError(t, err, "engine run")
	return st.RunID
}

// readEvents loads events.jsonl as []map for assertions.
func readEvents(t *testing.T, m *Manager, id, runID string) []map[string]any {
	t.Helper()
	data, err := os.ReadFile(m.Layout.WorkflowRunEvents(id, runID))
	require.NoError(t, err)
	out := []map[string]any{}
	dec := json.NewDecoder(newBytesReader(data))
	for dec.More() {
		var m map[string]any
		require.NoError(t, dec.Decode(&m))
		out = append(out, m)
	}
	return out
}

// ── 1. Manual single trigger → shell → end ─────────────────────────

func TestUseCase_ManualSingleTrigger_Shell(t *testing.T) {
	m := newMgr(t)
	require.NoError(t, m.Start(context.Background()))

	args := []string{"cmd", "/C", "echo single"}
	if runtime.GOOS != "windows" {
		args = []string{"sh", "-c", "echo single"}
	}

	w := workflow.Workflow{
		ID:      "uc-manual-shell",
		Version: 1,
		Name:    "Manual single trigger",
		Enabled: true,
		Triggers: []workflow.Trigger{
			{Type: workflow.TriggerManual, Label: "Run once"},
		},
		Graph: workflow.Graph{
			Entry: "step",
			Nodes: []workflow.Node{
				{ID: "step", Type: workflow.NodeShell, Command: args},
				{ID: "done", Type: workflow.NodeEnd, Result: "ok"},
			},
			Edges: []workflow.Edge{{From: "step", To: "done"}},
		},
	}

	runID := runWorkflow(t, m, w, workflow.Event{Type: string(workflow.TriggerManual), At: time.Now()})

	events := readEvents(t, m, w.ID, runID)
	require.NotEmpty(t, events)
	require.Equal(t, "workflow_started", events[0]["event"])
	require.Equal(t, "workflow_completed", events[len(events)-1]["event"])

	st, err := m.StateStore.Load(w.ID, runID)
	require.NoError(t, err)
	require.Equal(t, workflow.StatusSuccess, st.Status)
	require.Contains(t, st.Completed, "step")
	require.Contains(t, st.Completed, "done")
}

// ── 2. Multi-trigger (cron + manual + webhook) ─────────────────────

func TestUseCase_MultiTrigger_RouterRegistersAll(t *testing.T) {
	m := newMgr(t)
	require.NoError(t, m.Start(context.Background()))

	w := workflow.Workflow{
		ID:      "uc-multi-trigger",
		Version: 1,
		Name:    "Multi trigger",
		Enabled: true,
		Triggers: []workflow.Trigger{
			{Type: workflow.TriggerCron, Schedule: "0 8 * * *"},
			{Type: workflow.TriggerManual, Label: "run_now"},
			{Type: workflow.TriggerWebhook, Path: "/hooks/uc-multi"},
		},
		Graph: workflow.Graph{
			Entry: "end",
			Nodes: []workflow.Node{
				{ID: "end", Type: workflow.NodeEnd, Result: "triggered"},
			},
		},
	}
	require.NoError(t, m.Service.Create(w.ID, w, nil))
	require.NoError(t, HotReload(context.Background(), m.Service, m.Router, m.Cron, nil, w.ID))

	// Verify validator accepts the multi-trigger shape.
	loaded, err := m.Service.Load(w.ID)
	require.NoError(t, err)
	require.True(t, parse.Validate(loaded).Ok())
	require.Len(t, loaded.Triggers, 3)

	// Manual dispatch fires through router.
	manualEvt := workflow.Event{Type: string(workflow.TriggerManual), At: time.Now()}
	matched := m.Router.Dispatch(context.Background(), manualEvt)
	require.GreaterOrEqual(t, matched, 1, "manual event should match")

	// Webhook event with matching path also fires.
	webhookEvt := workflow.Event{
		Type: string(workflow.TriggerWebhook),
		At:   time.Now(),
		Payload: map[string]any{
			"path":   "/hooks/uc-multi",
			"method": "POST",
		},
	}
	matched = m.Router.Dispatch(context.Background(), webhookEvt)
	require.GreaterOrEqual(t, matched, 1, "webhook event should match")

	// Drain the worker briefly so we don't leak runs.
	time.Sleep(100 * time.Millisecond)
}

// ── 3. Template render through nodes ───────────────────────────────

func TestUseCase_TemplateRender_EventNodeEnv(t *testing.T) {
	m := newMgr(t)
	require.NoError(t, m.Start(context.Background()))

	args := []string{"cmd", "/C", "echo hello {{.Event.Payload.user}}"}
	if runtime.GOOS != "windows" {
		args = []string{"sh", "-c", "echo hello {{.Event.Payload.user}}"}
	}

	w := workflow.Workflow{
		ID:      "uc-template",
		Version: 1,
		Name:    "Template render",
		Enabled: true,
		Triggers: []workflow.Trigger{{Type: workflow.TriggerManual}},
		Graph: workflow.Graph{
			Entry: "echo",
			Nodes: []workflow.Node{
				{ID: "echo", Type: workflow.NodeShell, Command: args},
				{ID: "stop", Type: workflow.NodeEnd,
					Result: "echoed: {{.Node.echo.stdout}}"},
			},
			Edges: []workflow.Edge{{From: "echo", To: "stop"}},
		},
	}

	runID := runWorkflow(t, m, w, workflow.Event{
		Type:    string(workflow.TriggerManual),
		At:      time.Now(),
		Payload: map[string]any{"user": "alice"},
	})

	st, err := m.StateStore.Load(w.ID, runID)
	require.NoError(t, err)
	require.Equal(t, workflow.StatusSuccess, st.Status)
	stop, ok := st.Outputs["stop"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, stop["result"], "hello alice")
}

// ── 4. Connector node ──────────────────────────────────────────────

func TestUseCase_Connector_StubModule(t *testing.T) {
	m := newMgr(t)

	// Register a stub connector module so the connector executor has
	// something to dispatch to. Real prod path: RegisterLiveConnectors
	// copies from internal/connectors/.
	executed := false
	var receivedArgs map[string]string
	m.Connectors.Register(pkgconnector.Module{
		Meta: pkgconnector.Meta{Key: "stub", Name: "Stub", Description: "test only"},
		Operations: []pkgconnector.Operation{
			{
				Key:         "echo",
				Name:        "Echo",
				Description: "Echo back the input.",
				Execute: func(c *pkgconnector.Ctx) (any, error) {
					executed = true
					receivedArgs = map[string]string{"text": c.Input("text")}
					return map[string]any{"echoed": c.Input("text")}, nil
				},
			},
		},
	})

	require.NoError(t, m.Start(context.Background()))

	w := workflow.Workflow{
		ID:      "uc-connector",
		Version: 1,
		Name:    "Connector test",
		Enabled: true,
		Triggers: []workflow.Trigger{{Type: workflow.TriggerManual}},
		Graph: workflow.Graph{
			Entry: "call",
			Nodes: []workflow.Node{
				{
					ID:     "call",
					Type:   workflow.NodeConnector,
					Module: "stub",
					Op:     "echo",
					Args:   map[string]any{"text": "{{.Event.Payload.text}}"},
				},
				{ID: "stop", Type: workflow.NodeEnd},
			},
			Edges: []workflow.Edge{{From: "call", To: "stop"}},
		},
	}

	runID := runWorkflow(t, m, w, workflow.Event{
		Type:    string(workflow.TriggerManual),
		At:      time.Now(),
		Payload: map[string]any{"text": "hello from event"},
	})

	require.True(t, executed, "connector Execute should have fired")
	require.Equal(t, "hello from event", receivedArgs["text"])

	st, err := m.StateStore.Load(w.ID, runID)
	require.NoError(t, err)
	require.Equal(t, workflow.StatusSuccess, st.Status)
}

// ── 5. Router.RunNow bypass Enabled ───────────────────────────────

func TestUseCase_RunNow_BypassesDisabled(t *testing.T) {
	m := newMgr(t)
	require.NoError(t, m.Start(context.Background()))

	w := workflow.Workflow{
		ID:      "uc-disabled",
		Version: 1,
		Name:    "Disabled workflow",
		Enabled: false, // ← key: workflow disabled
		Triggers: []workflow.Trigger{{Type: workflow.TriggerManual}},
		Graph: workflow.Graph{
			Entry: "end",
			Nodes: []workflow.Node{
				{ID: "end", Type: workflow.NodeEnd, Result: "ran"},
			},
		},
	}
	require.NoError(t, m.Service.Create(w.ID, w, nil))
	require.NoError(t, HotReload(context.Background(), m.Service, m.Router, m.Cron, nil, w.ID))

	// Dispatch should NOT match a disabled workflow.
	matched := m.Router.Dispatch(context.Background(), workflow.Event{Type: string(workflow.TriggerManual)})
	require.Equal(t, 0, matched, "Dispatch must skip disabled workflows")

	// RunNow MUST work — it's the UI Run Now button's contract.
	err := m.Router.RunNow(context.Background(), w.ID, workflow.Event{
		Type: string(workflow.TriggerManual), At: time.Now(),
	})
	require.NoError(t, err)
	// Give worker a moment to drain.
	time.Sleep(200 * time.Millisecond)
}

// ── Helpers ────────────────────────────────────────────────────────

func newBytesReader(b []byte) *byteReader { return &byteReader{b: b} }

type byteReader struct {
	b []byte
	i int
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.i >= len(r.b) {
		return 0, errEOF
	}
	n := copy(p, r.b[r.i:])
	r.i += n
	return n, nil
}

var errEOF = newErr("EOF")

func newErr(s string) error { return &simpleErr{s} }

type simpleErr struct{ s string }

func (e *simpleErr) Error() string { return e.s }

// Avoid unused-import flake — connector pkg used implicitly through Manager.
var _ = connector.NewRegistry
var _ = filepath.Join
