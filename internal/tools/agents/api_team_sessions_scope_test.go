package agents

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

// sessionsAll calls GET /api/team/agents/{id}/sessions?scope=all as u.
func sessionsAll(t *testing.T, u *entity.User, agentID string) (int, teamAgentSessionsAll) {
	t.Helper()
	w, c := teamReq(t, u, http.MethodGet, "/api/team/agents/"+agentID+"/sessions?scope=all", nil, map[string]string{"id": agentID})
	apiTeamAgentSessions(c)
	var out teamAgentSessionsAll
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v %s", err, w.Body)
		}
	}
	return w.Code, out
}

// The Chats drawer's All tab: the owner and a current recipient see every
// chat of the shared agent and may open each other's; a stranger gets 403,
// and so does the recipient once the agent is unshared — including the
// owner's chat the recipient spoke in.
func TestTeamAgentSessionsScopeAll(t *testing.T) {
	withTeamWorld(t)
	owner, bob, carol := &entity.User{ID: "u1"}, &entity.User{ID: "bob"}, &entity.User{ID: "carol"}
	seedTeamProject(t, "p1", owner.ID)
	p := seedTeamAgent(t, owner.ID, "helper", "p1")
	chatOf := func(u *entity.User) string {
		w, c := teamReq(t, u, http.MethodPost, "/", map[string]any{}, map[string]string{"id": p.ID})
		apiTeamAgentChat(c)
		var out struct {
			SessionID string `json:"session_id"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		if w.Code != http.StatusOK || out.SessionID == "" {
			t.Fatalf("chat as %s: %d %s", u.ID, w.Code, w.Body)
		}
		return out.SessionID
	}
	ids := func(r teamAgentSessionsAll) map[string]TeamAgentSessionItem {
		m := map[string]TeamAgentSessionItem{}
		for _, s := range r.Sessions {
			m[s.ID] = s
		}
		return m
	}

	// Not shared yet: the owner may ask, sees only their own chats, no tabs.
	ownChat := chatOf(owner)
	code, r := sessionsAll(t, owner, p.ID)
	if code != http.StatusOK || r.Shared || len(r.Sessions) != 1 || !r.Sessions[0].Mine || r.Sessions[0].OwnerUserID != owner.ID {
		t.Fatalf("owner before share: %d %+v", code, r)
	}

	if code := shareWith(t, owner, p.ID, bob.ID); code != http.StatusOK {
		t.Fatalf("share: %d", code)
	}
	bobChat := chatOf(bob)

	// Owner: both chats, bob's marked as not theirs.
	code, r = sessionsAll(t, owner, p.ID)
	got := ids(r)
	if code != http.StatusOK || !r.Shared || len(got) != 2 || !got[ownChat].Mine || got[bobChat].Mine || got[bobChat].OwnerUserID != bob.ID {
		t.Fatalf("owner all: %d %+v", code, r)
	}
	// Recipient: both chats too.
	code, r = sessionsAll(t, bob, p.ID)
	got = ids(r)
	if code != http.StatusOK || !r.Shared || len(got) != 2 || !got[bobChat].Mine || got[ownChat].Mine {
		t.Fatalf("recipient all: %d %+v", code, r)
	}
	// Stranger: 403.
	if code, _ := sessionsAll(t, carol, p.ID); code != http.StatusForbidden {
		t.Fatalf("stranger all: %d, want 403", code)
	}

	// Opening each other's chat passes the same check; a stranger's does not.
	reg := globalMgr.Registry()
	ownSess, _ := reg.Session(ownChat)
	bobSess, _ := reg.Session(bobChat)
	_, oc := teamReq(t, owner, http.MethodGet, "/", nil, nil)
	_, bc := teamReq(t, bob, http.MethodGet, "/", nil, nil)
	_, cc := teamReq(t, carol, http.MethodGet, "/", nil, nil)
	if !ownsSession(oc, bobSess) || !ownsSession(bc, ownSess) {
		t.Fatal("owner/recipient cannot open each other's chat while shared")
	}
	if ownsSession(cc, ownSess) || ownsSession(cc, bobSess) {
		t.Fatal("stranger opens a chat of the shared agent")
	}
	// bob speaks in the owner's chat: he becomes a participant of it.
	if ownSess.Meta.AddParticipant(bob.ID) {
		if err := session.SaveMeta(globalLayout, ownChat, ownSess.Meta); err != nil {
			t.Fatal(err)
		}
		_ = globalMgr.RefreshSession(ownChat)
	}
	ownSess, _ = reg.Session(ownChat)
	if !ownSess.Meta.IsParticipant(bob.ID) {
		t.Fatal("participant not recorded")
	}

	// Unshare: 403 for bob, and neither side opens the other's chat.
	w, c := teamReq(t, owner, http.MethodDelete, "/", nil, map[string]string{"id": p.ID, "uid": bob.ID})
	apiTeamAgentShareRemove(c)
	if w.Code != http.StatusOK {
		t.Fatalf("unshare: %d", w.Code)
	}
	if code, _ := sessionsAll(t, bob, p.ID); code != http.StatusForbidden {
		t.Fatalf("recipient all after unshare: %d, want 403", code)
	}
	if ownsSession(bc, ownSess) {
		t.Fatal("bob still opens the owner's chat after unshare")
	}
	if ownsSession(oc, bobSess) {
		t.Fatal("the owner still opens bob's chat after unshare")
	}
	code, r = sessionsAll(t, owner, p.ID)
	if code != http.StatusOK || r.Shared || len(r.Sessions) != 1 || r.Sessions[0].ID != ownChat {
		t.Fatalf("owner all after unshare: %d %+v", code, r)
	}
}

// Audit: the All tab lists another person's chats only when they are web
// chats — the kind the share opens. The owner's Slack chat of the agent is
// neither listed for a recipient (no title/status leak) nor openable.
func TestTeamAgentSessionsScopeAllSkipsOthersChannelChats(t *testing.T) {
	withTeamWorld(t)
	owner, bob := &entity.User{ID: "u1"}, &entity.User{ID: "bob"}
	seedTeamProject(t, "p1", owner.ID)
	p := seedTeamAgent(t, owner.ID, "helper", "p1")
	if code := shareWith(t, owner, p.ID, bob.ID); code != http.StatusOK {
		t.Fatalf("share: %d", code)
	}
	if _, err := globalMgr.CreateSession(t.Context(), session.CreateOptions{
		ID: "owner-slack", ProjectID: "p1", Origin: session.OriginSlack, AgentID: p.ID, UserID: owner.ID,
	}); err != nil {
		t.Fatal(err)
	}
	if code, r := sessionsAll(t, bob, p.ID); code != http.StatusOK || len(r.Sessions) != 0 {
		t.Fatalf("recipient all lists the owner's Slack chat: %d %+v", code, r)
	}
	// The owner's own list is unchanged: their Slack chat is still theirs.
	if code, r := sessionsAll(t, owner, p.ID); code != http.StatusOK || len(r.Sessions) != 1 || r.Sessions[0].ID != "owner-slack" {
		t.Fatalf("owner all: %d %+v", code, r)
	}
}
