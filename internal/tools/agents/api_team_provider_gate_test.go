package agents

import (
	"context"
	"net/http"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/entity"
)

// A team-agent PATCH that names a provider the caller cannot pick is
// refused before anything is written: the persona (handle, project) and
// both projects' fields stay exactly as they were. The same request with
// a provider the caller may pick goes through.
func TestTeamAgentUpdateProviderDeniedLeavesAgentUntouched(t *testing.T) {
	withTeamWorld(t)
	prevTag, prevKnob := providerAccessTagAllows, adminSeeAllProviders
	t.Cleanup(func() { providerAccessTagAllows, adminSeeAllProviders = prevTag, prevKnob })
	providerAccessTagAllows = func(_ context.Context, _ *entity.User, _ provider.Type, name string) bool { return name == "mine" }
	adminSeeAllProviders = func() bool { return true }

	u := &entity.User{ID: "u1", Role: entity.RoleUser, Approved: true}
	seedTeamProject(t, "p-home", u.ID)
	seedTeamProject(t, "p-next", u.ID)
	p := seedTeamAgent(t, u.ID, "worker", "p-home")
	id := map[string]string{"id": p.ID}
	snap := func(pid string) (string, string) {
		proj, ok := globalMgr.Registry().Project(pid)
		if !ok {
			t.Fatalf("project %s gone", pid)
		}
		return proj.Meta.Description, proj.Meta.Defaults.Provider
	}
	homeDesc, homeProv := snap("p-home")
	nextDesc, nextProv := snap("p-next")

	body := map[string]any{
		"project_id":  "p-next",
		"handle":      "renamed",
		"description": "changed",
		"provider":    "claude/other",
	}
	w, c := teamReq(t, u, http.MethodPatch, "/api/team/agents/"+p.ID, body, id)
	apiTeamAgentUpdate(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("denied provider: status %d, want 403: %s", w.Code, w.Body)
	}
	got, err := globalTeam.Get(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ProjectID != "p-home" || got.Handle != p.Handle {
		t.Fatalf("persona changed by a refused update: project=%q handle=%q", got.ProjectID, got.Handle)
	}
	if d, pr := snap("p-home"); d != homeDesc || pr != homeProv {
		t.Fatalf("p-home changed: desc=%q provider=%q", d, pr)
	}
	if d, pr := snap("p-next"); d != nextDesc || pr != nextProv {
		t.Fatalf("p-next changed: desc=%q provider=%q", d, pr)
	}

	body["provider"] = "claude/mine"
	w, c = teamReq(t, u, http.MethodPatch, "/api/team/agents/"+p.ID, body, id)
	apiTeamAgentUpdate(c)
	if w.Code != http.StatusOK {
		t.Fatalf("allowed provider: status %d, want 200: %s", w.Code, w.Body)
	}
	got, _ = globalTeam.Get(context.Background(), p.ID)
	if got.ProjectID != "p-next" {
		t.Fatalf("allowed update did not move the agent: %q", got.ProjectID)
	}
	if _, pr := snap("p-next"); pr != "claude/mine" {
		t.Fatalf("allowed update did not set the provider: %q", pr)
	}
}

// Creating an agent on an existing project with a provider the caller
// cannot pick writes no agent row and leaves the project alone.
func TestTeamAgentCreateOnExistingProjectProviderDenied(t *testing.T) {
	withTeamWorld(t)
	prevTag, prevKnob := providerAccessTagAllows, adminSeeAllProviders
	t.Cleanup(func() { providerAccessTagAllows, adminSeeAllProviders = prevTag, prevKnob })
	providerAccessTagAllows = func(_ context.Context, _ *entity.User, _ provider.Type, name string) bool { return name == "mine" }
	adminSeeAllProviders = func() bool { return true }

	u := &entity.User{ID: "u1", Role: entity.RoleUser, Approved: true}
	seedTeamProject(t, "p-home", u.ID)
	w, c := teamReq(t, u, http.MethodPost, "/api/team/agents", map[string]any{
		"name": "Worker", "handle": "worker", "project_id": "p-home", "provider": "claude/other",
	}, nil)
	apiTeamAgentCreate(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403: %s", w.Code, w.Body)
	}
	if _, err := globalTeam.GetByHandle(context.Background(), u.ID, "worker"); err == nil {
		t.Fatal("a refused create still wrote the agent")
	}
	if proj, _ := globalMgr.Registry().Project("p-home"); proj.Meta.Defaults.Provider != "" {
		t.Fatalf("project provider changed: %q", proj.Meta.Defaults.Provider)
	}
}
