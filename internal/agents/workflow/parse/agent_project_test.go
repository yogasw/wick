package parse

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// An agent that opens its own session needs a project; one that shares a
// session_init's (or another node's) session does not.
func TestAgentProjectErrors(t *testing.T) {
	solo := func(n workflow.Node) workflow.Workflow {
		n.ID, n.Type, n.Label = "a", workflow.NodeAgent, "a"
		return workflow.Workflow{Graph: workflow.Graph{Nodes: []workflow.Node{n}}}
	}
	withInit := func(session, workspace string) workflow.Workflow {
		return workflow.Workflow{Graph: workflow.Graph{
			Nodes: []workflow.Node{
				{ID: "init", Type: workflow.NodeSessionInit, Label: "init"},
				{ID: "a", Type: workflow.NodeAgent, Label: "a", Session: session, Workspace: workspace},
			},
			Edges: []workflow.Edge{{From: "init", To: "a"}},
		}}
	}
	cases := []struct {
		name string
		w    workflow.Workflow
		want int
	}{
		{"no init, no project", solo(workflow.Node{}), 1},
		{"no init, project set", solo(workflow.Node{Workspace: "proj"}), 0},
		{"session_from another node", solo(workflow.Node{SessionFrom: "x"}), 0},
		{"after session_init", withInit("", ""), 0},
		{"after session_init but session new", withInit(workflow.SessionNew, ""), 1},
		{"session new with project", withInit(workflow.SessionNew, "proj"), 0},
	}
	for _, tc := range cases {
		if got := AgentProjectErrors(tc.w); len(got) != tc.want {
			t.Errorf("%s: %d errors, want %d (%v)", tc.name, len(got), tc.want, got)
		}
	}

	// Editing keeps working: Validate only warns, ValidatePublish refuses.
	w := solo(workflow.Node{Prompt: "hi"})
	w.Graph.Entry = "a"
	if r := Validate(w); !hasPath(r.Warnings, "graph.nodes[a].workspace") || hasPath(r.Errors, "graph.nodes[a].workspace") {
		t.Errorf("Validate: want a warning, no error; got errors=%v warnings=%v", r.Errors, r.Warnings)
	}
	if r := ValidatePublish(w); !hasPath(r.Errors, "graph.nodes[a].workspace") {
		t.Errorf("ValidatePublish must refuse an agent without a project; got %v", r.Errors)
	}
}

func hasPath(errs []Error, path string) bool {
	for _, e := range errs {
		if e.Path == path {
			return true
		}
	}
	return false
}
