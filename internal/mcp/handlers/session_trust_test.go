package handlers

import (
	"context"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/sessionworkspace"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// addTrustInstance creates a session with one workspace instance and
// returns the layout, the session id and the instance.
func addTrustInstance(t *testing.T) (agentconfig.Layout, string, sessionworkspace.Instance) {
	t.Helper()
	layout, sid := newTestLayout(t)
	inst, err := sessionworkspace.Add(layout, sid, sessionworkspace.Instance{
		BaseKey: "httprest", Label: "staging", Config: map[string]string{"base_url": "https://acme.example"},
	})
	if err != nil {
		t.Fatalf("add instance: %v", err)
	}
	return layout, sid, inst
}

func asUser(id string) context.Context {
	return login.WithUser(context.Background(), &entity.User{ID: id, Role: entity.RoleUser, Approved: true}, nil)
}

// A PAT caller with no session of its own passes session_id naming
// someone else's session: the instance (and the credentials in its
// config) must not resolve.
func TestSessionInstanceRefusedForSessionArgOfAnotherOwner(t *testing.T) {
	layout, victim, inst := addTrustInstance(t)
	SetSessionOwnerResolver(func(sid string) (string, bool) { return "user-owner", sid == victim })
	t.Cleanup(func() { SetSessionOwnerResolver(nil) })

	if _, ok, err := SessionInstanceForCaller(asUser("user-stranger"), layout, victim, inst.ID); ok || err == nil {
		t.Fatalf("stranger reached another session's instance: ok=%v err=%v", ok, err)
	}
	// Same through a header the caller typed themselves.
	ctx := WithUnverifiedSessionID(asUser("user-stranger"), victim)
	if _, ok, err := SessionInstanceForCaller(ctx, layout, victim, inst.ID); ok || err == nil {
		t.Fatalf("unverified header reached another session's instance: ok=%v err=%v", ok, err)
	}
	if SessionTrusted(ctx, victim) {
		t.Fatal("an unverified header must not be trusted")
	}
}

// Legitimate paths keep working: the session the credential named, the
// owner naming their own session by argument, and wick's own principal.
func TestSessionInstanceResolvesForLegitimateCallers(t *testing.T) {
	layout, sessA, inst := addTrustInstance(t)
	SetSessionOwnerResolver(func(sid string) (string, bool) { return "user-owner", sid == sessA })
	t.Cleanup(func() { SetSessionOwnerResolver(nil) })

	cases := map[string]context.Context{
		"credential-named session": WithSessionID(asUser("user-chatting"), sessA),
		"owner by argument":        asUser("user-owner"),
		"owner by own header":      WithUnverifiedSessionID(asUser("user-owner"), sessA),
		"wick internal principal":  WithSessionTrustAll(context.Background()),
	}
	for name, ctx := range cases {
		target, ok, err := SessionInstanceForCaller(ctx, layout, sessA, inst.ID)
		if err != nil || !ok || target == nil || target.Label != "staging" {
			t.Errorf("%s: ok=%v err=%v", name, ok, err)
		}
	}
	// A normal connector id never needs a session.
	if _, ok, err := SessionInstanceForCaller(asUser("user-stranger"), layout, sessA, "conn-row"); ok || err != nil {
		t.Errorf("row connector: ok=%v err=%v", ok, err)
	}
}
