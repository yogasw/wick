package nodes

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/workflow"
)

// The pool spawns whatever the session's agent entry names, so a node's
// provider only takes effect if it is written there. Before this, a
// workflow session was created with a blank entry and every agent node
// silently ran on the global default instance.
func TestAgentNodePersistsProvider(t *testing.T) {
	p, layout := newThinkingTestPool(t)
	e := NewAgentExecutor(nil, p, nil)
	id := "wf_adhoc_provider_test"
	if err := p.EnsureSession(context.Background(), id, "workflow", ""); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	n := workflow.Node{ID: "ask", Type: workflow.NodeAgent, Provider: "claude_support_ent"}
	prov := fakeProvider{name: "claude_support_ent", typ: "claude"}
	if err := e.persistAgentSessionConfig(id, n, prov); err != nil {
		t.Fatalf("persistAgentSessionConfig: %v", err)
	}
	s, err := session.Load(layout, id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got := ""
	for _, a := range s.Agents {
		if a.Name == "default" {
			got = a.Provider
		}
	}
	if got != "claude/claude_support_ent" {
		t.Errorf("agent provider = %q, want claude/claude_support_ent", got)
	}
}

// An empty provider field means "inherit", so a reused session must keep
// whatever it already resolved rather than being stamped with the
// registry default.
func TestAgentNodeWithoutProviderLeavesEntryAlone(t *testing.T) {
	p, layout := newThinkingTestPool(t)
	e := NewAgentExecutor(nil, p, nil)
	id := "wf_adhoc_provider_inherit"
	if err := p.EnsureSession(context.Background(), id, "workflow", ""); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	if err := session.AddAgent(layout, id, "default", "claude/enginer"); err != nil {
		t.Fatalf("add agent: %v", err)
	}
	n := workflow.Node{ID: "ask", Type: workflow.NodeAgent}
	if err := e.persistAgentSessionConfig(id, n, fakeProvider{name: "other", typ: "claude"}); err != nil {
		t.Fatalf("persistAgentSessionConfig: %v", err)
	}
	s, err := session.Load(layout, id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, a := range s.Agents {
		if a.Name == "default" && a.Provider != "claude/enginer" {
			t.Errorf("provider = %q, want it left at claude/enginer", a.Provider)
		}
	}
}
