package nodes

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

func runSwitch(t *testing.T, n workflow.Node, payload map[string]any) workflow.NodeOutput {
	t.Helper()
	rc := &workflow.RunContext{
		Workflow:    workflow.Workflow{ID: "wf-test"},
		Event:       workflow.Event{Type: "manual", Payload: payload},
		EnvValues:   map[string]string{},
		Secrets:     map[string]string{},
		NodeOutputs: map[string]workflow.NodeOutput{},
	}
	out, err := NewSwitchExecutor().Execute(context.Background(), n, rc)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return out
}

func TestSwitch_FirstMatchWins(t *testing.T) {
	out := runSwitch(t, workflow.Node{
		ID:   "s1",
		Type: workflow.NodeSwitch,
		Cases: []workflow.SwitchCase{
			{When: `{{index .Event.Payload "status"}} == "approved"`, Case: "approve"},
			{When: `{{index .Event.Payload "status"}} == "rejected"`, Case: "reject"},
		},
		DefaultCase: "review",
	}, map[string]any{"status": "rejected"})
	if out.Verdict != "reject" {
		t.Fatalf("want reject, got %q", out.Verdict)
	}
}

func TestSwitch_DefaultFallback(t *testing.T) {
	out := runSwitch(t, workflow.Node{
		ID:   "s2",
		Type: workflow.NodeSwitch,
		Cases: []workflow.SwitchCase{
			{When: `{{index .Event.Payload "status"}} == "approved"`, Case: "approve"},
		},
		DefaultCase: "review",
	}, map[string]any{"status": "pending"})
	if out.Verdict != "review" {
		t.Fatalf("want review, got %q", out.Verdict)
	}
}

func TestSwitch_NoMatchNoDefault(t *testing.T) {
	rc := &workflow.RunContext{
		Workflow:    workflow.Workflow{},
		Event:       workflow.Event{Payload: map[string]any{"x": 1}},
		NodeOutputs: map[string]workflow.NodeOutput{},
	}
	_, err := NewSwitchExecutor().Execute(context.Background(), workflow.Node{
		ID:    "s3",
		Type:  workflow.NodeSwitch,
		Cases: []workflow.SwitchCase{{When: `{{index .Event.Payload "x"}} == 2`, Case: "x2"}},
	}, rc)
	if err == nil {
		t.Fatal("want error when no match and no default")
	}
}

func TestSwitch_CatchAllRule(t *testing.T) {
	out := runSwitch(t, workflow.Node{
		ID:   "s4",
		Type: workflow.NodeSwitch,
		Cases: []workflow.SwitchCase{
			{When: `{{index .Event.Payload "x"}} == 99`, Case: "ninetynine"},
			{When: "", Case: "catchall"},
		},
	}, map[string]any{"x": 1})
	if out.Verdict != "catchall" {
		t.Fatalf("want catchall, got %q", out.Verdict)
	}
}

func TestSwitch_TruthyStringRule(t *testing.T) {
	out := runSwitch(t, workflow.Node{
		ID:   "s5",
		Type: workflow.NodeSwitch,
		Cases: []workflow.SwitchCase{
			{When: `{{index .Event.Payload "flag"}}`, Case: "on"},
		},
		DefaultCase: "off",
	}, map[string]any{"flag": "yes"})
	if out.Verdict != "on" {
		t.Fatalf("want on, got %q", out.Verdict)
	}
}

func TestSwitch_FalsyStringRule(t *testing.T) {
	out := runSwitch(t, workflow.Node{
		ID:   "s6",
		Type: workflow.NodeSwitch,
		Cases: []workflow.SwitchCase{
			{When: `{{index .Event.Payload "flag"}}`, Case: "on"},
		},
		DefaultCase: "off",
	}, map[string]any{"flag": "false"})
	if out.Verdict != "off" {
		t.Fatalf("want off, got %q", out.Verdict)
	}
}

func TestSwitch_EmptyCasesRejected(t *testing.T) {
	rc := &workflow.RunContext{Workflow: workflow.Workflow{}, NodeOutputs: map[string]workflow.NodeOutput{}}
	_, err := NewSwitchExecutor().Execute(context.Background(), workflow.Node{ID: "x", Type: workflow.NodeSwitch}, rc)
	if err == nil {
		t.Fatal("want error on no cases")
	}
}
