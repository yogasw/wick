package nodes

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/provider"
)

func denyRegistry(t *testing.T, p provider.Provider, gotOwner *string) *provider.Registry {
	t.Helper()
	reg := provider.NewRegistry()
	reg.Register(p)
	reg.SetAccessCheck(func(_ context.Context, owner, typ, name string) error {
		*gotOwner = owner
		return errors.New("owner has no access to provider " + typ + "/" + name)
	})
	return reg
}

// A node naming a provider the owner may not use fails before anything
// is spawned, and the gate is asked about the workflow's owner.
func TestAgentNodeRefusesProviderOwnerCannotAccess(t *testing.T) {
	var owner string
	reg := denyRegistry(t, fakeProvider{name: "ent", typ: "codex"}, &owner)
	e := NewAgentExecutor(reg, nil, nil)
	rc := &workflow.RunContext{Workflow: workflow.Workflow{ID: "wf", CreatedBy: "u1"}, RunID: "r1"}
	_, err := e.Execute(context.Background(), workflow.Node{ID: "a", Type: workflow.NodeAgent, Provider: "ent", Prompt: "hi"}, rc)
	if err == nil || !strings.Contains(err.Error(), "owner has no access to provider codex/ent") {
		t.Fatalf("err = %v, want owner access refusal", err)
	}
	if owner != "u1" {
		t.Errorf("gate asked about %q, want the workflow owner u1", owner)
	}
}

// The one-shot path runs the registry default when provider is empty, so
// that default is gated too.
func TestAgentNodeRefusesDefaultProviderOnOneShotPath(t *testing.T) {
	var owner string
	reg := denyRegistry(t, fakeProvider{name: "gem", typ: "gemini"}, &owner)
	e := NewAgentExecutor(reg, nil, nil)
	rc := &workflow.RunContext{Workflow: workflow.Workflow{ID: "wf", CreatedBy: "u1"}, RunID: "r1"}
	if _, err := e.Execute(context.Background(), workflow.Node{ID: "a", Type: workflow.NodeAgent, Prompt: "hi"}, rc); err == nil {
		t.Fatal("default provider ran despite the gate")
	}
}

func TestClassifyNodeRefusesProviderOwnerCannotAccess(t *testing.T) {
	var owner string
	reg := denyRegistry(t, fakeProvider{name: "ent", typ: "claude"}, &owner)
	e := NewClassifyExecutor(reg)
	rc := &workflow.RunContext{Workflow: workflow.Workflow{ID: "wf", CreatedBy: "u2"}, RunID: "r1"}
	_, err := e.Execute(context.Background(), workflow.Node{ID: "c", Type: workflow.NodeClassify, Provider: "ent", OutputCases: []string{"a", "b"}}, rc)
	if err == nil || !strings.Contains(err.Error(), "owner has no access") {
		t.Fatalf("err = %v, want owner access refusal", err)
	}
}

// No gate installed (tests, headless boots) = every provider runnable.
func TestRegistryCheckAccessWithoutGate(t *testing.T) {
	reg := provider.NewRegistry()
	if err := reg.CheckAccess(context.Background(), "u", fakeProvider{name: "x", typ: "claude"}); err != nil {
		t.Errorf("CheckAccess without gate = %v, want nil", err)
	}
}

// The node's model rides with its provider onto the session entry the
// pool spawns from.
func TestAgentNodePersistsModel(t *testing.T) {
	p, layout := newThinkingTestPool(t)
	e := NewAgentExecutor(nil, p, nil)
	id := "wf_adhoc_model_test"
	if err := p.EnsureSession(context.Background(), id, "workflow", ""); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	n := workflow.Node{ID: "ask", Type: workflow.NodeAgent, Provider: "ent", Model: "sonnet"}
	if err := e.persistAgentSessionConfig(id, n, fakeProvider{name: "ent", typ: "claude"}); err != nil {
		t.Fatalf("persistAgentSessionConfig: %v", err)
	}
	s, err := session.Load(layout, id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, a := range s.Agents {
		if a.Name == "default" && (a.Provider != "claude/ent" || a.ModelID != "sonnet") {
			t.Errorf("entry = %s / %q, want claude/ent / sonnet", a.Provider, a.ModelID)
		}
	}
}

// Without a provider the model is meaningless and must not touch a pin.
func TestAgentNodeModelIgnoredWithoutProvider(t *testing.T) {
	p, layout := newThinkingTestPool(t)
	e := NewAgentExecutor(nil, p, nil)
	id := "wf_adhoc_model_inherit"
	if err := p.EnsureSession(context.Background(), id, "workflow", ""); err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	if err := session.SetModelID(layout, id, "default", "opus"); err != nil {
		t.Fatalf("seed model: %v", err)
	}
	n := workflow.Node{ID: "ask", Type: workflow.NodeAgent, Model: "sonnet"}
	if err := e.persistAgentSessionConfig(id, n, fakeProvider{name: "ent", typ: "claude"}); err != nil {
		t.Fatalf("persistAgentSessionConfig: %v", err)
	}
	s, _ := session.Load(layout, id)
	for _, a := range s.Agents {
		if a.Name == "default" && a.ModelID != "opus" {
			t.Errorf("model = %q, want opus left alone", a.ModelID)
		}
	}
}
