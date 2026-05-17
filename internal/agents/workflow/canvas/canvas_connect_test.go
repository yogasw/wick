package canvas

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// branchWorkflow builds a minimal workflow whose entry node is a
// classify (branch source) node, which makes case-labelled edges valid.
func branchWorkflow(id string) workflow.Workflow {
	return workflow.Workflow{
		ID:      id,
		Name:    id,
		Enabled: false,
		Triggers: []workflow.Trigger{
			{Type: workflow.TriggerManual, EntryNode: "cls"},
		},
		Graph: workflow.Graph{
			Nodes: []workflow.Node{
				{
					ID:          "cls",
					Type:        workflow.NodeClassify,
					Prompt:      "classify",
					OutputCases: []string{"yes", "no"},
				},
				{ID: "n2", Type: workflow.NodeEnd},
				{ID: "n3", Type: workflow.NodeEnd},
			},
			Edges: []workflow.Edge{
				{From: "cls", To: "n2", Case: "default"},
			},
		},
	}
}

// TestConnect_FirstEdgeSucceeds verifies that a normal Connect call works.
func TestConnect_FirstEdgeSucceeds(t *testing.T) {
	svc := newStub("wf")
	// Add a second node so we have a valid target.
	w := svc.workflows["wf"]
	w.Graph.Nodes = append(w.Graph.Nodes, workflow.Node{ID: "end", Type: workflow.NodeEnd})
	svc.workflows["wf"] = w

	c := newCanvas(svc)

	got, err := c.Connect("wf", "start", "end", "")
	if err != nil {
		t.Fatalf("expected first Connect to succeed, got: %v", err)
	}
	if !hasEdge(got.Graph.Edges, "start", "end") {
		t.Error("expected start→end edge in returned workflow")
	}
}

// TestConnect_DuplicateEdgeRejected verifies that calling Connect twice
// with identical from/to/case returns an error on the second call and
// does not append a duplicate edge.
func TestConnect_DuplicateEdgeRejected(t *testing.T) {
	svc := newStub("wf")
	w := svc.workflows["wf"]
	w.Graph.Nodes = append(w.Graph.Nodes, workflow.Node{ID: "end", Type: workflow.NodeEnd})
	svc.workflows["wf"] = w

	c := newCanvas(svc)

	if _, err := c.Connect("wf", "start", "end", ""); err != nil {
		t.Fatalf("first Connect failed: %v", err)
	}

	_, err := c.Connect("wf", "start", "end", "")
	if err == nil {
		t.Fatal("expected second Connect to fail with duplicate edge error, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("expected 'already exists' in error message, got: %v", err)
	}

	// Confirm the graph still has exactly 1 edge (no duplicate was appended).
	saved, _ := svc.Load("wf")
	edgeCount := 0
	for _, e := range saved.Graph.Edges {
		if e.From == "start" && e.To == "end" && e.Case == "" {
			edgeCount++
		}
	}
	if edgeCount != 1 {
		t.Errorf("expected exactly 1 start→end edge, found %d", edgeCount)
	}
}

// TestConnect_DifferentCaseSameEndpoints verifies that two edges between
// the same pair of nodes are allowed when they carry different case labels
// (valid for classify/branch source nodes).
func TestConnect_DifferentCaseSameEndpoints(t *testing.T) {
	svc := newStub("wf")
	svc.workflows["wf"] = branchWorkflow("wf")

	c := newCanvas(svc)

	if _, err := c.Connect("wf", "cls", "n2", "yes"); err != nil {
		t.Fatalf("Connect case=yes failed: %v", err)
	}
	if _, err := c.Connect("wf", "cls", "n3", "no"); err != nil {
		t.Fatalf("Connect case=no failed: %v", err)
	}

	saved, _ := svc.Load("wf")
	yesFound, noFound := false, false
	for _, e := range saved.Graph.Edges {
		if e.From == "cls" && e.To == "n2" && e.Case == "yes" {
			yesFound = true
		}
		if e.From == "cls" && e.To == "n3" && e.Case == "no" {
			noFound = true
		}
	}
	if !yesFound || !noFound {
		t.Errorf("expected both case edges; yesFound=%v noFound=%v edges=%v",
			yesFound, noFound, saved.Graph.Edges)
	}
}
