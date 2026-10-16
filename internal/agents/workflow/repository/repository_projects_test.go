package repository

import (
	"testing"

	wf "github.com/yogasw/wick/internal/agents/workflow"
)

func TestCheckProjects(t *testing.T) {
	w := wf.Workflow{Graph: wf.Graph{Nodes: []wf.Node{
		{ID: "init", Type: wf.NodeSessionInit, Workspace: "mine"},
		{ID: "judge", Type: wf.NodeAgent, Workspace: "secret"},
		{ID: "other", Type: wf.NodeTransform, Workspace: "secret"}, // not an agent/init node: ignored
	}}}
	r := &Repo{}
	if err := r.checkProjects(w, "u1"); err != nil {
		t.Fatalf("no gate installed must allow everything, got %v", err)
	}
	r.WithProjectAccess(func(user, project string) bool { return project == "mine" })
	if err := r.checkProjects(w, "u1"); err == nil {
		t.Fatal("agent bound to a project the author cannot access must be refused")
	}
	w.Graph.Nodes[1].Workspace = "mine"
	if err := r.checkProjects(w, "u1"); err != nil {
		t.Fatalf("accessible projects must pass, got %v", err)
	}
	if err := r.checkProjects(w, ""); err != nil {
		t.Fatal("no editor identity (CLI/tests) skips the check")
	}
}
