package team

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/connectors"
)

// Sessions: ops-main and its sub-agent ops-child belong to agent "ops",
// cap-main to the Captain, plain to u1 without an agent, other to u2.
func dataScopeLayout(t *testing.T) config.Layout {
	t.Helper()
	ctx := context.Background()
	layout := config.NewLayout(t.TempDir())
	for _, p := range []project.CreateOptions{{ID: "p-ops", Name: "ops", OwnerUserID: "u1"}, {ID: "p-cap", Name: "cap", OwnerUserID: "u1"}, {ID: "p-u2", Name: "u2", OwnerUserID: "u2"}} {
		if _, err := project.Create(layout, p); err != nil {
			t.Fatal(err)
		}
	}
	for _, s := range []session.CreateOptions{
		{ID: "ops-main", AgentID: "ops", UserID: "u1", ProjectID: "p-ops"},
		{ID: "ops-child", ParentSessionID: "ops-main", UserID: "u1", ProjectID: "p-ops"},
		{ID: "cap-main", AgentID: "cap", UserID: "u1", ProjectID: "p-cap"},
		{ID: "plain", UserID: "u1"},
		{ID: "other", UserID: "u2", ProjectID: "p-u2"},
	} {
		if _, err := session.Create(ctx, layout, s); err != nil {
			t.Fatal(err)
		}
	}
	return layout
}

func agentCtx(id string, captain bool) context.Context {
	s := NewScope(nil, false, captain, nil)
	s.agentID = id
	return connectors.WithAgentScope(context.Background(), s)
}

func TestCheckSessionTarget(t *testing.T) {
	layout := dataScopeLayout(t)
	person := context.Background()
	ops, capt := agentCtx("ops", false), agentCtx("cap", true)
	cases := []struct {
		name    string
		ctx     context.Context
		calling string
		target  string
		ok      bool
	}{
		{"agent own session", ops, "ops-main", "ops-main", true},
		{"agent own sub-agent session", ops, "ops-main", "ops-child", true},
		{"agent other agent's session", ops, "ops-main", "cap-main", false},
		{"agent owner's plain session", ops, "ops-main", "plain", false},
		{"captain any owner session", capt, "cap-main", "ops-child", true},
		{"captain owner's plain session", capt, "cap-main", "plain", true},
		{"captain other user's session", capt, "cap-main", "other", false},
		{"person own session", person, "plain", "ops-main", true},
		{"person other user's session", person, "plain", "other", false},
		{"missing session", person, "plain", "nope", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckSessionTarget(tc.ctx, layout, "u1", tc.calling, tc.target)
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

func TestCheckProjectTarget(t *testing.T) {
	layout := dataScopeLayout(t)
	ops, capt := agentCtx("ops", false), agentCtx("cap", true)
	if err := CheckProjectTarget(ops, layout, "u1", "ops-main", "p-ops"); err != nil {
		t.Fatalf("agent's own project refused: %v", err)
	}
	if err := CheckProjectTarget(ops, layout, "u1", "ops-main", "p-cap"); err == nil {
		t.Fatal("agent reached another agent's project")
	}
	if err := CheckProjectTarget(capt, layout, "u1", "cap-main", "p-ops"); err != nil {
		t.Fatalf("captain refused an owner project: %v", err)
	}
	for _, ctx := range []context.Context{capt, context.Background()} {
		if err := CheckProjectTarget(ctx, layout, "u1", "plain", "p-u2"); err == nil {
			t.Fatal("another user's project reached")
		}
	}
}

func TestCheckOwnerRecipient(t *testing.T) {
	ops := agentCtx("ops", false)
	if CheckOwnerRecipient(ops, "u1", "u1") != nil || CheckOwnerRecipient(ops, "u1", "u2") == nil || CheckOwnerRecipient(ops, "", "u2") == nil {
		t.Fatal("agent recipient rule wrong")
	}
	if CheckOwnerRecipient(context.Background(), "u1", "u2") != nil {
		t.Fatal("people are not limited by the agent rule")
	}
}
