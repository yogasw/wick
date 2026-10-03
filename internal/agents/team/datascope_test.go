package team

import (
	"context"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// Sessions: ops-main and its sub-agent ops-child belong to agent "ops",
// cap-main to the Captain, plain to u1 without an agent, other to u2.
func dataScopeLayout(t *testing.T) config.Layout {
	t.Helper()
	ctx := context.Background()
	layout := config.NewLayout(t.TempDir())
	for _, p := range []project.CreateOptions{{ID: "p-ops", Name: "ops", OwnerUserID: "u1"}, {ID: "p-cap", Name: "cap", OwnerUserID: "u1"}, {ID: "p-u2", Name: "u2", OwnerUserID: "u2"}, {ID: "p-shared", Name: "shared", OwnerUserID: "u2", Tags: []string{"t-team"}}} {
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
	s.agentID, s.ownerID = id, "u1"
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

// People keep the rules they had before Team agents: an admin (or a user
// allowed to see all sessions) reaches other people's sessions and
// projects, and a project shared by tag is open to its members.
func TestDataScopePeopleRules(t *testing.T) {
	layout := dataScopeLayout(t)
	admin := login.WithUser(context.Background(), &entity.User{ID: "u1", Role: entity.RoleAdmin, Approved: true}, nil)
	member := login.WithUser(context.Background(), &entity.User{ID: "u1", Role: entity.RoleUser, Approved: true}, []string{"t-team"})
	if err := CheckSessionTarget(admin, layout, "u1", "plain", "other"); err != nil {
		t.Fatalf("admin refused another user's session: %v", err)
	}
	if err := CheckProjectTarget(admin, layout, "u1", "plain", "p-u2"); err != nil {
		t.Fatalf("admin refused another user's project: %v", err)
	}
	if err := CheckProjectTarget(member, layout, "u1", "plain", "p-shared"); err != nil {
		t.Fatalf("tag member refused a shared project: %v", err)
	}
	if err := CheckProjectTarget(member, layout, "u1", "plain", "p-u2"); err == nil {
		t.Fatal("tag member reached an unshared project")
	}
}

// Inside an agent session a call with no person behind it fails closed.
func TestDataScopeAgentWithoutCaller(t *testing.T) {
	layout := dataScopeLayout(t)
	capt := agentCtx("cap", true)
	if err := CheckSessionTarget(capt, layout, "", "cap-main", "ops-main"); err == nil {
		t.Fatal("agent call without a caller reached another session")
	}
	if err := CheckProjectTarget(capt, layout, "", "cap-main", "p-ops"); err == nil {
		t.Fatal("agent call without a caller reached another project")
	}
	// Its own session and project stay usable.
	if CheckSessionTarget(capt, layout, "", "cap-main", "cap-main") != nil || CheckProjectTarget(capt, layout, "", "cap-main", "p-cap") != nil {
		t.Fatal("own session/project refused")
	}
}

// The owner's admin / see-all rights never reach through an agent: the
// Captain of an admin owner stays within the owner's own sessions and
// projects, while the admin as a person keeps the bypass.
func TestDataScopeAgentIgnoresOwnerAdmin(t *testing.T) {
	layout := dataScopeLayout(t)
	admin := &entity.User{ID: "u1", Role: entity.RoleAdmin, Approved: true}
	capt := login.WithUser(agentCtx("cap", true), admin, nil)
	if err := CheckSessionTarget(capt, layout, "u1", "cap-main", "other"); err == nil {
		t.Fatal("captain of an admin owner reached another user's session")
	}
	if err := CheckProjectTarget(capt, layout, "u1", "cap-main", "p-u2"); err == nil {
		t.Fatal("captain of an admin owner reached another user's project")
	}
	if err := CheckSessionTarget(capt, layout, "u1", "cap-main", "ops-main"); err != nil {
		t.Fatalf("captain refused its owner's session: %v", err)
	}
	person := login.WithUser(context.Background(), admin, nil)
	if CheckSessionTarget(person, layout, "u1", "plain", "other") != nil || CheckProjectTarget(person, layout, "u1", "plain", "p-u2") != nil {
		t.Fatal("admin as a person lost the bypass")
	}
}
