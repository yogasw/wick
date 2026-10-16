package parse

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

func sessionInitGraph(agentSession, agentFrom string, withAgent bool) workflow.Graph {
	g := workflow.Graph{Nodes: []workflow.Node{
		{ID: "gate", Type: workflow.NodeTransform, Label: "gate"},
		{ID: "init", Type: workflow.NodeSessionInit, Label: "init", Preset: "new"},
	}}
	g.Edges = []workflow.Edge{{From: "gate", To: "init"}}
	if withAgent {
		g.Nodes = append(g.Nodes, workflow.Node{ID: "judge", Type: workflow.NodeAgent, Label: "judge", Session: agentSession, SessionFrom: agentFrom})
		g.Edges = append(g.Edges, workflow.Edge{From: "init", To: "judge"})
	}
	return g
}

func warnPaths(g workflow.Graph) map[string]string {
	out := map[string]string{}
	r := &Result{}
	nodes := map[string]workflow.Node{}
	for _, n := range g.Nodes {
		nodes[n.ID] = n
	}
	validateSessionInitUse(r, g, nodes)
	for _, w := range r.Warnings {
		out[w.Path] = w.Message
	}
	return out
}

func TestSessionInitUse_AgentWithNewSessionWarnsBothNodes(t *testing.T) {
	got := warnPaths(sessionInitGraph(workflow.SessionNew, "", true))
	if len(got) != 2 {
		t.Fatalf("want a warning on session_init and on the agent, got %v", got)
	}
	if m := got["graph.nodes[judge].session"]; !strings.Contains(m, `session "new"`) || !strings.Contains(m, "session_from") {
		t.Fatalf("agent warning should explain the cause and the fix, got %q", m)
	}
	if m := got["graph.nodes[init].session"]; !strings.Contains(m, `"judge"`) {
		t.Fatalf("session_init warning should name the agent, got %q", m)
	}
}

func TestSessionInitUse_NoAgentDownstreamWarns(t *testing.T) {
	got := warnPaths(sessionInitGraph("", "", false))
	if m := got["graph.nodes[init].session"]; !strings.Contains(m, "no agent node") {
		t.Fatalf("want a no-agent warning, got %v", got)
	}
}

func TestSessionInitUse_DefaultOrSessionFromIsQuiet(t *testing.T) {
	for _, g := range []workflow.Graph{
		sessionInitGraph("", "", true),
		sessionInitGraph(workflow.SessionNew, "init", true), // session_from wins over "new"
	} {
		if got := warnPaths(g); len(got) != 0 {
			t.Fatalf("correct wiring must not warn, got %v", got)
		}
	}
}

func TestSessionInitUse_AgentWorkspaceIgnoredWhenInheriting(t *testing.T) {
	g := sessionInitGraph("", "", true)
	for i := range g.Nodes {
		switch g.Nodes[i].ID {
		case "init":
			g.Nodes[i].Workspace = "proj-a"
		case "judge":
			g.Nodes[i].Workspace = "proj-b"
		}
	}
	got := warnPaths(g)
	if m := got["graph.nodes[judge].workspace"]; !strings.Contains(m, "ignored") || !strings.Contains(m, "proj-b") {
		t.Fatalf("want a workspace-ignored warning on the agent, got %v", got)
	}
	// Same project on both is harmless, and an agent with its own session may choose freely.
	for i := range g.Nodes {
		if g.Nodes[i].ID == "judge" {
			g.Nodes[i].Workspace = "proj-a"
		}
	}
	if got := warnPaths(g); len(got) != 0 {
		t.Fatalf("same workspace must not warn, got %v", got)
	}
	for i := range g.Nodes {
		if g.Nodes[i].ID == "judge" {
			g.Nodes[i].Workspace, g.Nodes[i].Session = "proj-b", workflow.SessionNew
		}
	}
	if got := warnPaths(g); got["graph.nodes[judge].workspace"] != "" {
		t.Fatalf("a session-new agent owns its project, got %v", got)
	}
}

func TestSessionInitUse_SessionFromAnotherNodeWarns(t *testing.T) {
	g := sessionInitGraph("", "gate", true) // session_from points at another node, not init
	got := warnPaths(g)
	if m := got["graph.nodes[judge].session"]; !strings.Contains(m, `"gate"`) || !strings.Contains(m, "session_from") {
		t.Fatalf("want a session_from warning on the agent, got %v", got)
	}
	if got["graph.nodes[init].session"] == "" {
		t.Fatalf("no agent uses init's session, so init must warn too, got %v", got)
	}
}

func TestSessionInitUse_OneUserOneOverrideOnlyWarnsTheOverride(t *testing.T) {
	g := sessionInitGraph("", "", true)
	g.Nodes = append(g.Nodes, workflow.Node{ID: "other", Type: workflow.NodeAgent, Label: "other", Session: workflow.SessionNew})
	g.Edges = append(g.Edges, workflow.Edge{From: "init", To: "other"})
	got := warnPaths(g)
	if got["graph.nodes[other].session"] == "" || got["graph.nodes[init].session"] != "" || got["graph.nodes[judge].session"] != "" {
		t.Fatalf("only the overriding agent should warn when another agent uses init's session, got %v", got)
	}
}
