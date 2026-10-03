package agents

import (
	"context"
	"net/http"
	"testing"

	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// seedTeamAgent stores an agent of owner linked to projectID.
func seedTeamAgent(t *testing.T, owner, handle, projectID string) entity.AgentPersona {
	t.Helper()
	p := &entity.AgentPersona{OwnerUserID: owner, Handle: handle, ProjectID: projectID, AllowedConnectors: "[]"}
	if err := globalTeam.Create(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return *p
}

// Someone else's agent answers 404 on every {id} route, as if it did not
// exist, rather than 403 that would confirm it does.
func TestTeamAgentOfAnotherOwnerIs404(t *testing.T) {
	withTeamWorld(t)
	seedTeamProject(t, "p2", "u2")
	other := seedTeamAgent(t, "u2", "theirs", "p2")
	u := &entity.User{ID: "u1"}
	id := map[string]string{"id": other.ID}

	for name, h := range map[string]func(*tool.Ctx){
		"update": apiTeamAgentUpdate,
		"delete": apiTeamAgentDelete,
		"chat":   apiTeamAgentChat,
	} {
		w, c := teamReq(t, u, http.MethodPost, "/api/team/agents/"+other.ID, map[string]any{}, id)
		h(c)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", name, w.Code)
		}
	}
	if _, err := globalTeam.Get(context.Background(), other.ID); err != nil {
		t.Fatal("the other owner's agent must survive")
	}
}

// A grant outside the caller's catalog and an unknown run_as are 400s,
// refused before any project is made for the new agent.
func TestTeamAgentCreateRejectsBadInput(t *testing.T) {
	withTeamWorld(t)
	u := &entity.User{ID: "u1"}
	cases := map[string]map[string]any{
		"grant outside catalog": {
			"handle":             "worker",
			"allowed_connectors": []team.ConnectorGrant{{ConnectorID: "not-mine", Level: team.LevelAll}},
		},
		"invalid run_as": {"handle": "worker", "run_as": "root"},
	}
	for name, body := range cases {
		w, c := teamReq(t, u, http.MethodPost, "/api/team/agents", body, nil)
		apiTeamAgentCreate(c)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", name, w.Code, w.Body)
		}
	}
	if n := len(globalMgr.Registry().Projects()); n != 0 {
		t.Fatalf("refused creates left %d projects behind", n)
	}
	if rows, _ := globalTeam.List(context.Background(), u.ID); len(rows) != 0 {
		t.Fatalf("refused creates stored %d agents", len(rows))
	}

	// run_as on PATCH is validated the same way.
	seedTeamProject(t, "p1", u.ID)
	mine := seedTeamAgent(t, u.ID, "mine", "p1")
	w, c := teamReq(t, u, http.MethodPatch, "/api/team/agents/"+mine.ID, map[string]any{"run_as": "root"}, map[string]string{"id": mine.ID})
	apiTeamAgentUpdate(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("patch run_as: status %d, want 400", w.Code)
	}
}
