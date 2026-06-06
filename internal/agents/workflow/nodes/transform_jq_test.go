package nodes

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

func runTransform(t *testing.T, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	t.Helper()
	if rc == nil {
		rc = &workflow.RunContext{Workflow: workflow.Workflow{ID: "wf"}, NodeOutputs: map[string]workflow.NodeOutput{}}
	}
	return NewTransformExecutor().Execute(context.Background(), n, rc)
}

func TestTransform_JQ_FieldAccess(t *testing.T) {
	out, err := runTransform(t, workflow.Node{
		Type: workflow.NodeTransform, Engine: "jq",
		Input: `{"a":{"b":5}}`, Expression: ".a.b",
	}, nil)
	if err != nil {
		t.Fatalf("jq: %v", err)
	}
	if out.Result != float64(5) {
		t.Fatalf("want 5, got %v (%T)", out.Result, out.Result)
	}
}

func TestTransform_JQ_MultipleOutputsBecomeArray(t *testing.T) {
	out, err := runTransform(t, workflow.Node{
		Type: workflow.NodeTransform, Engine: "jq",
		Input: `[1,2,3]`, Expression: ".[]",
	}, nil)
	if err != nil {
		t.Fatalf("jq: %v", err)
	}
	arr, ok := out.Result.([]any)
	if !ok || len(arr) != 3 {
		t.Fatalf("want 3-element array, got %v (%T)", out.Result, out.Result)
	}
}

func TestTransform_JQ_ObjectConstruction(t *testing.T) {
	out, err := runTransform(t, workflow.Node{
		Type: workflow.NodeTransform, Engine: "jq",
		Input: `{"x":1,"y":2}`, Expression: "{sum: (.x + .y)}",
	}, nil)
	if err != nil {
		t.Fatalf("jq: %v", err)
	}
	m, ok := out.Result.(map[string]any)
	if !ok || m["sum"] != float64(3) {
		t.Fatalf("want {sum:3}, got %v (%T)", out.Result, out.Result)
	}
}

func TestTransform_JQ_DefaultInputIsRenderCtx(t *testing.T) {
	rc := &workflow.RunContext{
		Workflow:    workflow.Workflow{ID: "wf"},
		Event:       workflow.Event{Type: "manual"},
		NodeOutputs: map[string]workflow.NodeOutput{},
	}
	out, err := runTransform(t, workflow.Node{
		Type: workflow.NodeTransform, Engine: "jq", Expression: ".Event.type",
	}, rc)
	if err != nil {
		t.Fatalf("jq: %v", err)
	}
	if out.Result != "manual" {
		t.Fatalf("want \"manual\", got %v (%T)", out.Result, out.Result)
	}
}

func TestTransform_JQ_ParseError(t *testing.T) {
	if _, err := runTransform(t, workflow.Node{
		Type: workflow.NodeTransform, Engine: "jq",
		Input: `{}`, Expression: ".a |",
	}, nil); err == nil {
		t.Fatal("expected parse error for invalid jq program")
	}
}

func TestTransform_JQ_InputNotJSON(t *testing.T) {
	if _, err := runTransform(t, workflow.Node{
		Type: workflow.NodeTransform, Engine: "jq",
		Input: `not json`, Expression: ".",
	}, nil); err == nil {
		t.Fatal("expected error for non-JSON input")
	}
}

func TestTransform_JSONPath_ErrorsNotSilent(t *testing.T) {
	if _, err := runTransform(t, workflow.Node{
		Type: workflow.NodeTransform, Engine: "jsonpath",
		Input: `{"a":1}`, Expression: "$.a",
	}, nil); err == nil {
		t.Fatal("jsonpath should error explicitly, not silently return input")
	}
}
