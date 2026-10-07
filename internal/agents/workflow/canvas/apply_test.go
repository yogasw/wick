package canvas

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// TestApply_RunsStepsInOrderInOneSave covers the reason Apply exists: a
// node added earlier in the batch can be connected, patched and moved by
// later steps of the same call, and the result lands as one draft.
func TestApply_RunsStepsInOrderInOneSave(t *testing.T) {
	svc := newStub("wf")
	c := newCanvas(svc)

	got, err := c.Apply("wf", []EditOp{
		{Op: "add_node", Node: &workflow.Node{ID: "done", Type: workflow.NodeEnd}},
		{Op: "connect", From: "start", To: "done"},
		{Op: "update_node", NodeID: "done", Patch: map[string]any{"description": "Finished.\n\nWhy: marks the end."}},
		{Op: "move", Moves: []NodeMove{{NodeID: "start", X: 200, Y: 100}, {NodeID: "done", X: 200, Y: 280}}},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !hasEdge(got.Graph.Edges, "start", "done") {
		t.Error("expected start→done edge")
	}
	var desc string
	for _, n := range got.Graph.Nodes {
		if n.ID == "done" {
			desc = n.Description
		}
	}
	if !strings.Contains(desc, "Why:") {
		t.Errorf("patch not applied, description = %q", desc)
	}
	pos, _ := got.Canvas["positions"].(map[string]any)
	if _, ok := pos["done"]; !ok {
		t.Error("expected a position for done")
	}
	if !hasEdge(svc.workflows["wf"].Graph.Edges, "start", "done") {
		t.Error("batch was not saved")
	}
}

// TestApply_FailingStepSavesNothing: one bad step rejects the whole batch,
// so the draft never holds half an edit, and the error names the step.
func TestApply_FailingStepSavesNothing(t *testing.T) {
	svc := newStub("wf")
	c := newCanvas(svc)

	_, err := c.Apply("wf", []EditOp{
		{Op: "add_node", Node: &workflow.Node{ID: "done", Type: workflow.NodeEnd}},
		{Op: "connect", From: "start", To: "missing"},
	})
	if err == nil {
		t.Fatal("expected an error for a connect to a missing node")
	}
	if !strings.Contains(err.Error(), "ops[1] connect") {
		t.Errorf("error should name the failing step, got %v", err)
	}
	for _, n := range svc.workflows["wf"].Graph.Nodes {
		if n.ID == "done" {
			t.Fatal("first step was saved although the batch failed")
		}
	}
}

func TestApply_RejectsEmptyAndUnknownOps(t *testing.T) {
	c := newCanvas(newStub("wf"))
	if _, err := c.Apply("wf", nil); err == nil {
		t.Error("expected an error for an empty batch")
	}
	if _, err := c.Apply("wf", []EditOp{{Op: "rename"}}); err == nil || !strings.Contains(err.Error(), "unknown op") {
		t.Errorf("expected unknown op error, got %v", err)
	}
}
