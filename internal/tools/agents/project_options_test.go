package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/entity"
)

// seedProjectOptionsWorld lays out the projects every options test reads:
// the caller's own, someone else's, an ownerless legacy one, and a Team
// agent project on each side.
func seedProjectOptionsWorld(t *testing.T, me string) {
	t.Helper()
	withTeamWorld(t)
	seedTeamProject(t, "mine", me)
	seedTeamProject(t, "theirs", "u-other")
	seedTeamProject(t, "legacy", "")
	for id, owner := range map[string]string{"agent-mine": me, "agent-theirs": "u-other"} {
		if _, err := globalMgr.CreateProject(context.Background(), project.CreateOptions{
			ID: id, Name: id, OwnerUserID: owner, Tags: []string{project.AgentTag},
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func getProjectOptions(t *testing.T, u *entity.User, target string) map[string]map[string]any {
	t.Helper()
	w, c := teamReq(t, u, http.MethodGet, target, nil, nil)
	projectOptionsJSON(c)
	if w.Code != http.StatusOK {
		t.Fatalf("%s: status %d: %s", target, w.Code, w.Body.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	for _, r := range rows {
		out[r["id"].(string)] = r
	}
	return out
}

// An admin is offered what they are in — their own projects, their own
// agent's project, and the ownerless ones only admins reach — never
// somebody else's project or agent.
func TestProjectOptionsAdminSeesOnlyOwn(t *testing.T) {
	admin := &entity.User{ID: "u-admin", Role: entity.RoleAdmin, Approved: true}
	seedProjectOptionsWorld(t, admin.ID)
	got := getProjectOptions(t, admin, "/projects/options")
	for _, id := range []string{"mine", "agent-mine", "legacy"} {
		if got[id] == nil {
			t.Errorf("admin options miss %q: %v", id, got)
		}
	}
	for _, id := range []string{"theirs", "agent-theirs"} {
		if got[id] != nil {
			t.Errorf("admin options leak %q", id)
		}
	}
	if len(got) != 3 {
		t.Errorf("admin options = %d rows, want 3", len(got))
	}
}

// A Team agent project reaches the picker only for the agent's owner, even
// when a grant puts it in the caller's access set; hide_team drops the
// caller's own too.
func TestProjectOptionsHidesOthersAgentProjects(t *testing.T) {
	u := &entity.User{ID: "u1", Role: entity.RoleUser, Approved: true}
	seedProjectOptionsWorld(t, u.ID)
	// A cached grant stands in for a tag that reaches the other agent.
	grantCache.Store(u.ID, grantCacheEntry{set: map[string]struct{}{"agent-theirs": {}, "theirs": {}}, expiry: time.Now().Add(time.Hour)})
	t.Cleanup(func() { grantCache.Delete(u.ID) })

	got := getProjectOptions(t, u, "/projects/options")
	if got["agent-theirs"] != nil {
		t.Error("another user's agent project must not be offered")
	}
	if got["agent-mine"] == nil || got["mine"] == nil || got["theirs"] == nil {
		t.Errorf("options = %v, want mine, agent-mine and the granted theirs", got)
	}
	if got["theirs"]["shared"] != true {
		t.Errorf("granted project should read as shared: %v", got["theirs"])
	}
	if got["legacy"] != nil {
		t.Error("ownerless project is admin-only")
	}

	hidden := getProjectOptions(t, u, "/projects/options?hide_team=1")
	if hidden["agent-mine"] != nil {
		t.Error("hide_team must drop the caller's own agent project")
	}
}

// include= still returns a project the caller cannot reach (an agent
// project too), flagged no_access, and an unknown id is ignored.
func TestProjectOptionsIncludeForcesProject(t *testing.T) {
	u := &entity.User{ID: "u1", Role: entity.RoleUser, Approved: true}
	seedProjectOptionsWorld(t, u.ID)
	got := getProjectOptions(t, u, "/projects/options?hide_team=1&include=theirs,agent-theirs,ghost")
	for _, id := range []string{"theirs", "agent-theirs"} {
		if got[id] == nil || got[id]["no_access"] != true {
			t.Errorf("forced %q = %v, want present and no_access", id, got[id])
		}
	}
	if got["ghost"] != nil {
		t.Error("an unknown include id must not produce a row")
	}
	if _, flagged := got["mine"]["no_access"]; flagged {
		t.Errorf("own project must not be flagged: %v", got["mine"])
	}
}

// path is a host filesystem path: sent for the caller's own projects (and
// to an admin), blank for anything else.
func TestProjectOptionsPathOnlyForOwner(t *testing.T) {
	u := &entity.User{ID: "u1", Role: entity.RoleUser, Approved: true}
	seedProjectOptionsWorld(t, u.ID)
	got := getProjectOptions(t, u, "/projects/options?include=theirs")
	if got["mine"]["path"] == "" {
		t.Error("owner must get the path of their own project")
	}
	if got["theirs"]["path"] != "" {
		t.Errorf("path leaked for a project the caller does not own: %v", got["theirs"]["path"])
	}

	admin := &entity.User{ID: "u-admin", Role: entity.RoleAdmin, Approved: true}
	got = getProjectOptions(t, admin, "/projects/options?include=theirs")
	if got["theirs"]["path"] == "" {
		t.Error("admin should still get the path")
	}
}

// A project the caller may open but is not offered as a pick (another
// person's agent project reached through a tag) still resolves by id with
// no_access=false, so the project page and a chat's ticket affordances work.
func TestProjectOptionsIncludeOfAnOpenableProjectIsNotNoAccess(t *testing.T) {
	u := &entity.User{ID: "u1", Role: entity.RoleUser, Approved: true}
	seedProjectOptionsWorld(t, u.ID)
	grantCache.Store(u.ID, grantCacheEntry{set: map[string]struct{}{"agent-theirs": {}}, expiry: time.Now().Add(time.Hour)})
	t.Cleanup(func() { grantCache.Delete(u.ID) })

	if got := getProjectOptions(t, u, "/projects/options"); got["agent-theirs"] != nil {
		t.Fatalf("not a pick: agent-theirs listed without include")
	}
	got := getProjectOptions(t, u, "/projects/options?include=agent-theirs,theirs")
	if got["agent-theirs"] == nil {
		t.Fatal("include=agent-theirs: missing")
	}
	if _, flagged := got["agent-theirs"]["no_access"]; flagged {
		t.Errorf("openable project flagged no_access: %v", got["agent-theirs"])
	}
	if got["theirs"]["no_access"] != true {
		t.Errorf("unreachable project must stay no_access: %v", got["theirs"])
	}
}
