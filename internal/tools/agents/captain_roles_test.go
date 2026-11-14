package agents

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/team"
	teamagents "github.com/yogasw/wick/internal/connectors/team-agents"
)

// agents.roles_get / roles_set: read anytime; a change only on the
// owner's own request, from the rev it was read at, never the manager
// itself or another owner's agent.
func TestCaptainRoles(t *testing.T) {
	w := withCaptainWorld(t)
	ctx := context.Background()
	ops := CaptainOps{}

	got, err := ops.RolesGet(ctx, "s-captain", "@worker")
	if err != nil {
		t.Fatal(err)
	}
	m := got.(map[string]any)
	if m["rev"] != 0 || m["enabled"] != false {
		t.Fatalf("roles_get = %+v", m)
	}
	doc := m["doc"].(team.RBACDoc)
	doc.Rules = append([]team.RBACRule{{Match: team.MatchUser, Value: "u7", Role: "trusted"}}, doc.Rules...)
	doc.Lend["n1"] = map[string]team.RBACLevel{"member": {Level: team.LevelRead}}

	if _, err := ops.RolesSet(ctx, "s-captain", teamagents.RolesSetInput{Agent: "@captain", Doc: doc}); err == nil || !strings.Contains(err.Error(), "OTHER agents") {
		t.Errorf("roles_set on itself: %v", err)
	}
	if _, err := ops.RolesSet(ctx, "s-captain", teamagents.RolesSetInput{Agent: w.stranger.ID, Doc: doc}); err == nil {
		t.Error("roles_set on another owner's agent")
	}
	out, err := ops.RolesSet(ctx, "s-captain", teamagents.RolesSetInput{Agent: "@worker", Doc: doc, Reason: "trust u7"})
	if err != nil || out.(map[string]any)["rev"] != 1 || out.(map[string]any)["note"] == nil {
		t.Fatalf("roles_set = %v, %v", out, err)
	}
	if _, err := ops.RolesSet(ctx, "s-captain", teamagents.RolesSetInput{Agent: "@worker", Doc: doc, BaseRev: 0}); err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Errorf("stale roles_set: %v", err)
	}
	saved, rev, _ := globalTeam.RBAC(ctx, w.worker.ID)
	if rev != 1 || saved.Lend["n1"]["member"].Level != team.LevelRead {
		t.Fatalf("saved = %+v rev %d", saved, rev)
	}

	// Someone else's words as the latest turn: no change.
	line := `{"role":"user","source":"slack","sender":{"id":"U9","channel":"slack","wick_user_id":"u9"}}` + "\n"
	if err := os.WriteFile(globalLayout.SessionConversation("s-captain"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ops.RolesSet(ctx, "s-captain", teamagents.RolesSetInput{Agent: "@worker", Doc: doc, BaseRev: 1}); err == nil || !strings.Contains(err.Error(), "only the owner") {
		t.Errorf("roles_set on a non-owner's turn: %v", err)
	}
}

// team.roles / roles_set: an agent reads and changes its OWN roles, on
// its owner's own request only; a co-owner and a guest lend are valid.
func TestOwnRoles(t *testing.T) {
	w := withCaptainWorld(t)
	ctx := context.Background()

	got, err := OwnRolesGet(ctx, w.captain.ID)
	if err != nil || got.(map[string]any)["rev"] != 0 {
		t.Fatalf("roles = %v, %v", got, err)
	}
	doc := `{"roles":{"member":{"rank":40,"lend":"read"}},"rules":[{"match":"user","value":"u7","role":"owner"}],"lend":{"n1":{"guest":"all"}}}`
	out, err := OwnRolesSet(ctx, "s-captain", w.captain.ID, doc, 0, "co-owner + guest music")
	if err != nil || out.(map[string]any)["rev"] != 1 {
		t.Fatalf("roles_set = %v, %v", out, err)
	}
	saved, _, _ := globalTeam.RBAC(ctx, w.captain.ID)
	if !saved.HasCoOwner() || saved.LendLevel(team.RoleGuest, "n1").Level != team.LevelAll {
		t.Fatalf("saved = %+v", saved)
	}
	if _, err := OwnRolesSet(ctx, "s-captain", w.captain.ID, "{not json", 1, ""); err == nil {
		t.Error("bad JSON saved")
	}

	// Someone else's words as the latest turn: refused, whatever they ask.
	line := `{"role":"user","source":"slack","sender":{"id":"U9","channel":"slack","wick_user_id":"u9"}}` + "\n"
	if err := os.WriteFile(globalLayout.SessionConversation("s-captain"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := OwnRolesSet(ctx, "s-captain", w.captain.ID, doc, 1, ""); err == nil || !strings.Contains(err.Error(), "only this agent's owner") {
		t.Errorf("roles_set on a non-owner's turn: %v", err)
	}
}

// wick_roles: the session's own agent; the owner sees the document, a
// non-owner's turn only its own role.
func TestSessionRoles(t *testing.T) {
	w := withCaptainWorld(t)
	ctx := context.Background()
	if TeamCallerOf(ctx, "s-captain") != w.captain.ID {
		t.Skip("session is not bound to the captain in this fixture")
	}
	got, err := SessionRolesGet(ctx, "s-captain")
	if err != nil {
		t.Fatal(err)
	}
	if m := got.(map[string]any); m["doc"] == nil || m["your_role"] != team.RoleOwner {
		t.Fatalf("owner view = %+v", m)
	}
	line := `{"role":"user","source":"slack","sender":{"id":"U9","channel":"slack","wick_user_id":"u9"}}` + "\n"
	if err := os.WriteFile(globalLayout.SessionConversation("s-captain"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = SessionRolesGet(ctx, "s-captain")
	if err != nil {
		t.Fatal(err)
	}
	if m := got.(map[string]any); m["doc"] != nil {
		t.Fatalf("a non-owner saw the document: %+v", m)
	}
	if _, err := SessionRolesSet(ctx, "s-captain", "{}", 0, ""); err == nil {
		t.Fatal("a non-owner's turn changed roles")
	}
}
