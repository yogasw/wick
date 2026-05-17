package nodes

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/workflow"
)

func newTestRC() *workflow.RunContext {
	return &workflow.RunContext{
		Workflow: workflow.Workflow{ID: "it-ops"},
		RunID:    "0d147eca-82ca-48e9-8d11-8d3eaa926865",
		Outputs:  map[string]any{},
		Event:    workflow.Event{Type: "manual", Payload: map[string]any{}},
	}
}

func TestSessionInit_DefaultPreset_ResolvesToRunScopedID(t *testing.T) {
	rc := newTestRC()
	exec := NewSessionInitExecutor(nil)
	n := workflow.Node{ID: "session-init", Type: workflow.NodeSessionInit}

	out, err := exec.Execute(context.Background(), n, rc)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	want := "wf_it-ops_run_0d147eca-82ca-48e9-8d11-8d3eaa926865"
	if got, _ := out.Result.(string); got != want {
		t.Errorf("sessionID = %q, want %q", got, want)
	}
	if rc.DefaultAgentSessionID != want {
		t.Errorf("rc.DefaultAgentSessionID = %q, want %q", rc.DefaultAgentSessionID, want)
	}
	if rc.AgentSessionIDs["session-init"] != want {
		t.Errorf("rc.AgentSessionIDs missing entry for node")
	}
}

func TestSessionInit_PresetWorkflowGlobal(t *testing.T) {
	rc := newTestRC()
	exec := NewSessionInitExecutor(nil)
	n := workflow.Node{ID: "si", Type: workflow.NodeSessionInit, Preset: workflow.SessionPresetWorkflowGlobal}

	out, err := exec.Execute(context.Background(), n, rc)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := "wf_it-ops"
	if got, _ := out.Result.(string); got != want {
		t.Errorf("sessionID = %q, want %q", got, want)
	}
}

func TestSessionInit_PresetNew_ProducesUUID(t *testing.T) {
	rc := newTestRC()
	exec := NewSessionInitExecutor(nil)
	n := workflow.Node{ID: "si", Type: workflow.NodeSessionInit, Preset: workflow.SessionPresetNew}

	out1, _ := exec.Execute(context.Background(), n, rc)
	out2, _ := exec.Execute(context.Background(), n, rc)

	id1, _ := out1.Result.(string)
	id2, _ := out2.Result.(string)
	if id1 == id2 {
		t.Errorf("preset=new should yield distinct IDs, both = %q", id1)
	}
	if !strings.HasPrefix(id1, "wf_adhoc_") {
		t.Errorf("expected wf_adhoc_ prefix, got %q", id1)
	}
}

func TestSessionInit_CustomIDTemplate_RendersFromEvent(t *testing.T) {
	rc := newTestRC()
	rc.Event.Payload["thread_ts"] = "1715167891.234567"

	exec := NewSessionInitExecutor(nil)
	n := workflow.Node{
		ID:        "si",
		Type:      workflow.NodeSessionInit,
		SessionID: "slack-{{.Event.Payload.thread_ts}}",
	}

	out, err := exec.Execute(context.Background(), n, rc)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := "slack-1715167891.234567"
	if got, _ := out.Result.(string); got != want {
		t.Errorf("sessionID = %q, want %q", got, want)
	}
}

func TestSessionInit_CustomIDWinsOverPreset(t *testing.T) {
	rc := newTestRC()
	exec := NewSessionInitExecutor(nil)
	n := workflow.Node{
		ID:        "si",
		Type:      workflow.NodeSessionInit,
		Preset:    workflow.SessionPresetWorkflowGlobal,
		SessionID: "literal-id",
	}

	out, err := exec.Execute(context.Background(), n, rc)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got, _ := out.Result.(string); got != "literal-id" {
		t.Errorf("sessionID = %q, want literal-id", got)
	}
}

func TestSessionInit_UnknownPreset_Errors(t *testing.T) {
	rc := newTestRC()
	exec := NewSessionInitExecutor(nil)
	n := workflow.Node{ID: "si", Type: workflow.NodeSessionInit, Preset: "bogus"}

	if _, err := exec.Execute(context.Background(), n, rc); err == nil {
		t.Fatal("expected error for unknown preset")
	}
}

func TestSessionInit_EmptyRenderedID_Errors(t *testing.T) {
	rc := newTestRC()
	exec := NewSessionInitExecutor(nil)
	n := workflow.Node{ID: "si", Type: workflow.NodeSessionInit, SessionID: "{{.Event.Payload.missing}}"}

	if _, err := exec.Execute(context.Background(), n, rc); err == nil {
		t.Fatal("expected error for empty rendered sessionID")
	}
}

// Storage validator gates sessionIDs at the pool boundary — make sure
// every default-resolved ID survives that check. The original bug:
// "wf:id:run:uuid" was rejected because `:` is not in the allowed
// charset. Underscores are.
func TestDefaultRunSessionID_PassesStorageValidator(t *testing.T) {
	cases := []struct {
		id    string
		runID string
	}{
		{"it-ops", "0d147eca-82ca-48e9-8d11-8d3eaa926865"},
		{"support_triage", "abc"},
		{"abc123", "x"},
	}
	for _, c := range cases {
		sid := DefaultRunSessionID(c.id, c.runID)
		if err := storage.ValidateSessionID(sid); err != nil {
			t.Errorf("id=%q runID=%q → %q: %v", c.id, c.runID, sid, err)
		}
	}
}

func TestSessionInit_AllPresets_PassStorageValidator(t *testing.T) {
	rc := newTestRC()
	exec := NewSessionInitExecutor(nil)
	for _, preset := range []string{
		"", // default → workflow_run
		workflow.SessionPresetWorkflowRun,
		workflow.SessionPresetWorkflowGlobal,
		workflow.SessionPresetNew,
	} {
		n := workflow.Node{ID: "si", Type: workflow.NodeSessionInit, Preset: preset}
		out, err := exec.Execute(context.Background(), n, rc)
		if err != nil {
			t.Fatalf("preset=%q: Execute: %v", preset, err)
		}
		id, _ := out.Result.(string)
		if err := storage.ValidateSessionID(id); err != nil {
			t.Errorf("preset=%q produced invalid sessionID %q: %v", preset, id, err)
		}
	}
}
