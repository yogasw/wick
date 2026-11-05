package canvas

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// builtAroundStart is a workflow assembled over MCP: the trigger enters at
// its own node and the scaffold's "start" is left dangling.
func builtAroundStart() workflow.Workflow {
	return workflow.Workflow{
		ID: "wf-a",
		Triggers: []workflow.Trigger{
			{ID: "trigger-channel", Type: workflow.TriggerChannel, EntryNode: "check"},
		},
		Graph: workflow.Graph{
			Entry: "start",
			Nodes: []workflow.Node{
				{ID: "start", Type: workflow.NodeEnd, Result: "ok"},
				{ID: "check", Type: workflow.NodeGoScript, Code: "package main\nfunc main() {}\n"},
				{ID: "done", Type: workflow.NodeEnd, Result: "done"},
			},
			Edges: []workflow.Edge{{From: "check", To: "done"}},
		},
		Canvas: map[string]any{"positions": map[string]any{
			"start": map[string]any{"x": 0, "y": 0},
			"check": map[string]any{"x": 0, "y": 200},
		}},
	}
}

func hasNode(w workflow.Workflow, id string) bool {
	for _, n := range w.Graph.Nodes {
		if n.ID == id {
			return true
		}
	}
	return false
}

func TestPruneUnusedScaffoldStart_RemovesDanglingPlaceholder(t *testing.T) {
	w := builtAroundStart()
	if !pruneUnusedScaffoldStart(&w) {
		t.Fatal("dangling scaffold start must be removed")
	}
	if hasNode(w, "start") {
		t.Error("start still in graph")
	}
	if w.Graph.Entry != "" {
		t.Errorf("graph.entry = %q, want cleared", w.Graph.Entry)
	}
	if _, ok := w.Canvas["positions"].(map[string]any)["start"]; ok {
		t.Error("start position must be dropped with the node")
	}
	if !hasNode(w, "check") || !hasNode(w, "done") {
		t.Error("other nodes must be untouched")
	}
}

// Every way a "start" can still be in use keeps it.
func TestPruneUnusedScaffoldStart_KeepsStartInUse(t *testing.T) {
	cases := map[string]func(*workflow.Workflow){
		"edge into start (used as the final end)": func(w *workflow.Workflow) {
			w.Graph.Edges = append(w.Graph.Edges, workflow.Edge{From: "check", To: "start"})
		},
		"trigger enters at start": func(w *workflow.Workflow) {
			w.Triggers[0].EntryNode = "start"
		},
		"trigger falls back to graph.entry": func(w *workflow.Workflow) {
			w.Triggers = append(w.Triggers, workflow.Trigger{ID: "trigger-manual", Type: workflow.TriggerManual})
		},
		"fallback points at start": func(w *workflow.Workflow) {
			w.Graph.Nodes[1].Fallback = "start"
		},
		"template reads its output": func(w *workflow.Workflow) {
			w.Graph.Nodes[1].Code = `// uses {{ .Node.start.result }}`
		},
		"not an end node": func(w *workflow.Workflow) {
			w.Graph.Nodes[0].Type = workflow.NodeGoScript
		},
		"nothing else in the graph yet": func(w *workflow.Workflow) {
			w.Graph.Nodes = w.Graph.Nodes[:1]
			w.Graph.Edges = nil
			w.Triggers[0].EntryNode = ""
			w.Graph.Entry = "other"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			w := builtAroundStart()
			mutate(&w)
			if pruneUnusedScaffoldStart(&w) {
				t.Fatal("start is in use and must be kept")
			}
			if !hasNode(w, "start") {
				t.Fatal("start removed")
			}
		})
	}
}

// A fresh scaffold (start is the manual trigger's entry) must not change.
func TestPruneUnusedScaffoldStart_FreshScaffoldUntouched(t *testing.T) {
	w := workflow.Workflow{
		Triggers: []workflow.Trigger{{ID: "trigger-manual", Type: workflow.TriggerManual, EntryNode: "start"}},
		Graph: workflow.Graph{
			Entry: "start",
			Nodes: []workflow.Node{{ID: "start", Type: workflow.NodeEnd, Result: "ok"}},
		},
	}
	if pruneUnusedScaffoldStart(&w) {
		t.Fatal("fresh scaffold must keep its start node")
	}
}
