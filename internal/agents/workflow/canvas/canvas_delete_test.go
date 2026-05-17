package canvas

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// entryWorkflow builds a workflow where graph.Entry is explicitly set to
// "start", giving us a concrete node to attempt deleting.
func entryWorkflow(id string) workflow.Workflow {
	return workflow.Workflow{
		ID:      id,
		Name:    id,
		Enabled: false,
		Triggers: []workflow.Trigger{
			{Type: workflow.TriggerManual},
		},
		Graph: workflow.Graph{
			Entry: "start",
			Nodes: []workflow.Node{
				{ID: "start", Type: workflow.NodeShell, Command: []string{"echo", "hi"}},
				{ID: "other", Type: workflow.NodeEnd},
			},
			Edges: []workflow.Edge{
				{From: "start", To: "other"},
			},
		},
	}
}

// TestDeleteNode_EntryNodeRejected verifies that attempting to delete the
// graph's explicit entry node returns a clear, actionable error before any
// mutation occurs.
func TestDeleteNode_EntryNodeRejected(t *testing.T) {
	svc := newStub()
	svc.workflows["wf"] = entryWorkflow("wf")
	c := newCanvas(svc)

	_, err := c.DeleteNode("wf", "start")
	if err == nil {
		t.Fatal("expected error when deleting entry node, got nil")
	}
	if !strings.Contains(err.Error(), "cannot delete entry node") {
		t.Errorf("error message should contain %q, got: %s", "cannot delete entry node", err.Error())
	}
}

// TestDeleteNode_NonEntryNodeSucceeds verifies that a non-entry node can
// be deleted without error and is absent from the returned graph.
func TestDeleteNode_NonEntryNodeSucceeds(t *testing.T) {
	svc := newStub()
	svc.workflows["wf"] = entryWorkflow("wf")
	c := newCanvas(svc)

	got, err := c.DeleteNode("wf", "other")
	if err != nil {
		t.Fatalf("unexpected error deleting non-entry node: %v", err)
	}
	if _, ok := findNode(got.Graph.Nodes, "other"); ok {
		t.Error("deleted node still present in graph")
	}
	if got.Graph.Entry != "start" {
		t.Errorf("entry node changed unexpectedly: %q", got.Graph.Entry)
	}
}

// TestDeleteNode_CleansUpEdges verifies that all edges touching the deleted
// node are pruned from the graph.
func TestDeleteNode_CleansUpEdges(t *testing.T) {
	svc := newStub()
	svc.workflows["wf"] = entryWorkflow("wf")
	c := newCanvas(svc)

	got, err := c.DeleteNode("wf", "other")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, e := range got.Graph.Edges {
		if e.From == "other" || e.To == "other" {
			t.Errorf("edge involving deleted node still present: %+v", e)
		}
	}
}
