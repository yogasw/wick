package agents

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// The meta names the Team agent of a web chat with one, so a ticket's
// chat row can open that agent's chat.
func TestSessionMetaNamesTeamAgent(t *testing.T) {
	withTeamWorld(t)
	owner := &entity.User{ID: "u1"}
	seedTeamProject(t, "p1", owner.ID)
	p := seedTeamAgent(t, owner.ID, "helper", "p1")
	chat := openChat(t, owner, p.ID, false)

	read := func(sid string) SessionMetaDTO {
		t.Helper()
		w, c := teamReq(t, owner, http.MethodGet, "/", nil, map[string]string{"id": sid})
		sessionAccessMW(sharedChatReadOnlyMW(apiSessionMeta))(c)
		if w.Code != http.StatusOK {
			t.Fatalf("meta %s: %d %s", sid, w.Code, w.Body)
		}
		var dto SessionMetaDTO
		if err := json.Unmarshal(w.Body.Bytes(), &dto); err != nil {
			t.Fatal(err)
		}
		return dto
	}
	if got := read(chat).AgentID; got != p.ID {
		t.Fatalf("agent chat: agent_id %q, want %q", got, p.ID)
	}
}
