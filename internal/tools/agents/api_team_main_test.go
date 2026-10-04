package agents

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yogasw/wick/internal/entity"
)

// openChat opens u's main chat with agentID, or a new one.
func openChat(t *testing.T, u *entity.User, agentID string, fresh bool) string {
	t.Helper()
	w, c := teamReq(t, u, http.MethodPost, "/", map[string]any{"new": fresh}, map[string]string{"id": agentID})
	apiTeamAgentChat(c)
	if w.Code != http.StatusOK {
		t.Fatalf("open chat: %d %s", w.Code, w.Body)
	}
	var out struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return out.SessionID
}

func setMain(u *entity.User, t *testing.T, agentID, sessionID string) int {
	w, c := teamReq(t, u, http.MethodPost, "/", map[string]any{"session_id": sessionID}, map[string]string{"id": agentID})
	apiTeamAgentSetMain(c)
	return w.Code
}

func TestTeamAgentSetMainChat(t *testing.T) {
	withTeamWorld(t)
	owner, bob := &entity.User{ID: "u1"}, &entity.User{ID: "bob"}
	seedTeamProject(t, "p1", owner.ID)
	p := seedTeamAgent(t, owner.ID, "helper", "p1")

	oldMain := openChat(t, owner, p.ID, false)
	fresh := openChat(t, owner, p.ID, true)
	if s, _ := mainSessionOf(owner.ID, p.ID); s.ID != oldMain {
		t.Fatalf("main before pin = %q, want %q", s.ID, oldMain)
	}

	// Someone without the agent cannot pin, nor pin another's chat.
	if code := setMain(bob, t, p.ID, fresh); code != http.StatusNotFound {
		t.Fatalf("stranger pin: %d, want 404", code)
	}
	if code := setMain(owner, t, p.ID, "no-such-chat"); code != http.StatusNotFound {
		t.Fatalf("unknown chat pin: %d, want 404", code)
	}

	if code := setMain(owner, t, p.ID, fresh); code != http.StatusOK {
		t.Fatalf("pin: %d", code)
	}
	if s, _ := mainSessionOf(owner.ID, p.ID); s.ID != fresh {
		t.Fatalf("main after pin = %q, want %q", s.ID, fresh)
	}
	// The old main is still there, as an ordinary chat; one main only.
	mains, kept := 0, false
	for _, s := range agentSessions(owner.ID, p.ID) {
		if s.Meta.AgentMain {
			mains++
		}
		kept = kept || (s.ID == oldMain && !s.Meta.AgentMain)
	}
	if mains != 1 || !kept {
		t.Fatalf("mains = %d, old main kept as chat = %v", mains, kept)
	}
	// Opening the agent now lands in the pinned chat.
	if got := openChat(t, owner, p.ID, false); got != fresh {
		t.Fatalf("open after pin = %q, want %q", got, fresh)
	}
}
