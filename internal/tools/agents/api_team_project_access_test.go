package agents

import (
	"net/http"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// An owner who lost access to the agent's project may neither open a chat
// on it nor edit the agent in place; moving it to a project they reach
// still works.
func TestTeamAgentProjectAccessRechecked(t *testing.T) {
	withTeamWorld(t)
	u := &entity.User{ID: "u1"}
	seedTeamProject(t, "p-lost", "u2") // owned by someone else, no tag grant
	seedTeamProject(t, "p-mine", u.ID)
	p := seedTeamAgent(t, u.ID, "worker", "p-lost")
	id := map[string]string{"id": p.ID}

	w, c := teamReq(t, u, http.MethodPost, "/api/team/agents/"+p.ID+"/chat", map[string]any{}, id)
	apiTeamAgentChat(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("chat: status %d, want 403", w.Code)
	}

	w, c = teamReq(t, u, http.MethodPatch, "/api/team/agents/"+p.ID, map[string]any{"description": "x"}, id)
	apiTeamAgentUpdate(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("patch in place: status %d, want 403", w.Code)
	}

	w, c = teamReq(t, u, http.MethodPatch, "/api/team/agents/"+p.ID, map[string]any{"project_id": "p-mine"}, id)
	apiTeamAgentUpdate(c)
	if w.Code != http.StatusOK {
		t.Fatalf("move to own project: status %d, want 200: %s", w.Code, w.Body)
	}
}
