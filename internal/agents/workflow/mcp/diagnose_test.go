package mcp

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
)

// failedRun builds a minimal RunState with an error attached so each
// rule's regex has something to bite on. Tests only set the fields
// the rule actually reads.
func failedRun(node, msg string) workflow.RunState {
	return workflow.RunState{
		RunID:      "r1",
		WorkflowID: "wf1",
		Status:     "failed",
		Completed:  []string{"start"},
		Outputs:    map[string]any{},
		Event:      workflow.Event{Type: "channel", Channel: "slack", Payload: map[string]any{"text": "hi", "user": "U1", "channel_id": "C1"}},
		Error:      &workflow.NodeError{Node: node, Message: msg},
	}
}

func TestDiagnoseSuccessRun(t *testing.T) {
	m := &Ops{}
	st := workflow.RunState{Status: "success", Completed: []string{"a", "b"}}
	d := m.Diagnose(context.Background(), workflow.Workflow{}, st)
	if d.ErrorClass != "" {
		t.Fatalf("success run got ErrorClass=%q, want empty", d.ErrorClass)
	}
	if d.Status != "success" {
		t.Fatalf("Status=%q", d.Status)
	}
	if len(d.PathTaken) != 2 || d.PathTaken[0] != "a" {
		t.Fatalf("PathTaken=%v", d.PathTaken)
	}
}

func TestDiagnoseTemplateMissingKey(t *testing.T) {
	m := &Ops{}
	st := failedRun("send_msg",
		`template execute: template: node:1:7: executing "node" at <.Node.trigger.payload.channel>: map has no entry for key "channel"`)
	// Inject a "trigger" output so availableKeysForPath can drill.
	st.Outputs["trigger"] = map[string]any{
		"payload": map[string]any{
			"text":       "hi",
			"user":       "U1",
			"channel_id": "C1",
		},
	}
	d := m.Diagnose(context.Background(), workflow.Workflow{}, st)
	if d.ErrorClass != "template_missing_key" {
		t.Fatalf("ErrorClass=%q", d.ErrorClass)
	}
	if d.SuggestedFix == nil {
		t.Fatalf("expected SuggestedFix")
	}
	if d.SuggestedFix.Suggested != "{{.Node.trigger.payload.channel_id}}" {
		t.Fatalf("Suggested=%q", d.SuggestedFix.Suggested)
	}
	// channel → channel_id is Levenshtein distance 3 → low per the
	// confidence table. AI surfaces the suggestion + lets user decide.
	if d.SuggestedFix.Confidence != "low" {
		t.Fatalf("unexpected confidence %q", d.SuggestedFix.Confidence)
	}
	if len(d.AvailableKeys) == 0 {
		t.Fatalf("AvailableKeys should list sibling keys")
	}
}

func TestDiagnoseSecretLeak(t *testing.T) {
	m := &Ops{}
	st := failedRun("http_call",
		`template execute: secret leak: .Env.API_TOKEN is secret-tagged`)
	d := m.Diagnose(context.Background(), workflow.Workflow{}, st)
	if d.ErrorClass != "secret_leak_guard" {
		t.Fatalf("ErrorClass=%q", d.ErrorClass)
	}
	if d.SuggestedFix == nil || d.SuggestedFix.Suggested != "{{.Secret.API_TOKEN}}" {
		t.Fatalf("SuggestedFix=%+v", d.SuggestedFix)
	}
	if d.SuggestedFix.Confidence != "high" {
		t.Fatalf("confidence=%q want high", d.SuggestedFix.Confidence)
	}
}

func TestDiagnoseChannelActionMissing(t *testing.T) {
	ir := integration.New()
	ir.RegisterAction(integration.ActionDescriptor{Channel: "slack", Action: "send_message"})
	ir.RegisterAction(integration.ActionDescriptor{Channel: "slack", Action: "update_message"})
	m := &Ops{Integration: ir}
	st := failedRun("notify",
		`channel action "slack.sednmessage" not registered`)
	d := m.Diagnose(context.Background(), workflow.Workflow{}, st)
	if d.ErrorClass != "channel_action_missing" {
		t.Fatalf("ErrorClass=%q", d.ErrorClass)
	}
	if d.SuggestedFix == nil || d.SuggestedFix.Suggested != "send_message" {
		t.Fatalf("SuggestedFix=%+v", d.SuggestedFix)
	}
}

func TestDiagnoseBranchNoEdge(t *testing.T) {
	m := &Ops{}
	w := workflow.Workflow{
		Graph: workflow.Graph{
			Edges: []workflow.Edge{
				{From: "route", To: "bug_handler", Case: "bug"},
				{From: "route", To: "feature_handler", Case: "feature"},
			},
		},
	}
	st := failedRun("route",
		`branch route: no edge matched verdict "bugs"`)
	d := m.Diagnose(context.Background(), w, st)
	if d.ErrorClass != "branch_no_edge_matched" {
		t.Fatalf("ErrorClass=%q", d.ErrorClass)
	}
	if d.SuggestedFix == nil || d.SuggestedFix.Suggested != "bug" {
		t.Fatalf("SuggestedFix=%+v", d.SuggestedFix)
	}
	if len(d.AvailableKeys) != 2 {
		t.Fatalf("AvailableKeys=%v", d.AvailableKeys)
	}
}

func TestDiagnoseAgentSessionInvalid(t *testing.T) {
	m := &Ops{}
	w := workflow.Workflow{
		Graph: workflow.Graph{
			Nodes: []workflow.Node{
				{ID: "summarize", Type: workflow.NodeAgent},
				{ID: "extract", Type: workflow.NodeAgent},
			},
		},
	}
	st := failedRun("followup",
		`session_from references nonexistent node "summrize"`)
	d := m.Diagnose(context.Background(), w, st)
	if d.ErrorClass != "agent_session_invalid" {
		t.Fatalf("ErrorClass=%q", d.ErrorClass)
	}
	if d.SuggestedFix == nil || d.SuggestedFix.Suggested != "summarize" {
		t.Fatalf("SuggestedFix=%+v", d.SuggestedFix)
	}
}

func TestDiagnoseUnknown(t *testing.T) {
	m := &Ops{}
	st := failedRun("some_node", "completely unrecognised disaster")
	d := m.Diagnose(context.Background(), workflow.Workflow{}, st)
	if d.ErrorClass != "unknown" {
		t.Fatalf("ErrorClass=%q", d.ErrorClass)
	}
	if d.Summary == "" {
		t.Fatalf("expected raw error in Summary")
	}
}

func TestConfidenceFor(t *testing.T) {
	cases := []struct {
		typo, guess, want string
	}{
		{"channel", "channel", "high"},
		{"chnl", "chnl", "high"},
		{"sednmessage", "send_message", "medium"}, // distance 2
		{"send_msg", "send_message", "low"},       // distance 4
		{"channel", "channelx", "high"},           // distance 1
		{"", "x", "low"},
	}
	for _, c := range cases {
		got := confidenceFor(c.typo, c.guess)
		if got != c.want {
			t.Fatalf("confidenceFor(%q,%q)=%q want %q", c.typo, c.guess, got, c.want)
		}
	}
}
