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
