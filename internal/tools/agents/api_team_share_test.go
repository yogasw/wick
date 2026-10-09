package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// rosterOf reads user u's roster.
func rosterOf(t *testing.T, u *entity.User) []TeamAgentItem {
	t.Helper()
	w, c := teamReq(t, u, http.MethodGet, "/api/team/agents?ensure=0", nil, nil)
	apiTeamAgentList(c)
	if w.Code != http.StatusOK {
		t.Fatalf("roster: %d %s", w.Code, w.Body)
	}
	var out struct {
		Agents []TeamAgentItem `json:"agents"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Agents
}

func shareWith(t *testing.T, u *entity.User, agentID, userID string) int {
	t.Helper()
	w, c := teamReq(t, u, http.MethodPost, "/api/team/agents/"+agentID+"/shares", map[string]any{"user_id": userID}, map[string]string{"id": agentID})
	apiTeamAgentShareAdd(c)
	return w.Code
}

func TestTeamAgentShareLifecycle(t *testing.T) {
	withTeamWorld(t)
	owner, bob, carol := &entity.User{ID: "u1"}, &entity.User{ID: "bob"}, &entity.User{ID: "carol"}
	seedTeamProject(t, "p1", owner.ID)
	p := seedTeamAgent(t, owner.ID, "helper", "p1")
	id := map[string]string{"id": p.ID}

	// Only the owner manages shares: bob cannot share it to himself.
	if code := shareWith(t, bob, p.ID, bob.ID); code != http.StatusNotFound {
		t.Fatalf("stranger share: %d, want 404", code)
	}
	if code := shareWith(t, owner, p.ID, owner.ID); code != http.StatusBadRequest {
		t.Fatalf("share with self: %d, want 400", code)
	}
	if code := shareWith(t, owner, p.ID, bob.ID); code != http.StatusOK {
		t.Fatalf("share: %d", code)
	}
	w, c := teamReq(t, owner, http.MethodGet, "/", nil, id)
	apiTeamAgentShares(c)
	var list struct {
		Shares    []teamShareItem `json:"shares"`
		Shareable bool            `json:"shareable"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != http.StatusOK || len(list.Shares) != 1 || list.Shares[0].UserID != bob.ID || !list.Shareable {
		t.Fatalf("shares = %d %s", w.Code, w.Body)
	}

	// bob sees it as a viewer, without the owner's settings; carol does not.
	r := rosterOf(t, bob)
	if len(r) != 1 || r[0].ID != p.ID || r[0].Role != RoleViewer || r[0].SharedByID != owner.ID {
		t.Fatalf("bob roster = %+v", r)
	}
	if r[0].SystemPrompt != "" || len(r[0].AllowedConnectors) != 0 {
		t.Fatalf("viewer item leaks settings: %+v", r[0])
	}
	if len(rosterOf(t, carol)) != 0 {
		t.Fatal("carol sees an agent not shared with her")
	}
	if own := rosterOf(t, owner); len(own) != 1 || own[0].Role != "" {
		t.Fatalf("owner roster = %+v", own)
	}

	// Every edit is 403 for bob; chat works; carol gets 404 everywhere.
	for name, h := range map[string]func(*tool.Ctx){
		"update": apiTeamAgentUpdate, "delete": apiTeamAgentDelete,
		"shares": apiTeamAgentShareAdd, "scheduled": apiTeamAgentScheduledList,
	} {
		w, c := teamReq(t, bob, http.MethodPost, "/", map[string]any{"user_id": carol.ID, "disabled": true}, id)
		h(c)
		if name == "scheduled" && w.Code == http.StatusServiceUnavailable {
			continue // no schedule store in this world
		}
		if w.Code != http.StatusForbidden {
			t.Errorf("%s as recipient: %d, want 403", name, w.Code)
		}
		w, c = teamReq(t, carol, http.MethodPost, "/", map[string]any{}, id)
		h(c)
		if w.Code != http.StatusNotFound && !(name == "scheduled" && w.Code == http.StatusServiceUnavailable) {
			t.Errorf("%s as stranger: %d, want 404", name, w.Code)
		}
	}
	w, c = teamReq(t, carol, http.MethodPost, "/", map[string]any{}, id)
	apiTeamAgentChat(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("stranger chat: %d", w.Code)
	}
	w, c = teamReq(t, bob, http.MethodPost, "/", map[string]any{}, id)
	apiTeamAgentChat(c)
	if w.Code != http.StatusOK {
		t.Fatalf("recipient chat: %d %s", w.Code, w.Body)
	}
	var chat struct {
		SessionID string `json:"session_id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &chat)
	sess, ok := globalMgr.Registry().Session(chat.SessionID)
	if !ok || sess.Meta.UserID != bob.ID || sess.Meta.AgentID != p.ID {
		t.Fatalf("recipient session = %+v", sess.Meta)
	}
	// It runs as the owner, and only bob may open it.
	if got := team.SpawnIdentity(sess.Meta, &p, bob.ID); got != owner.ID {
		t.Fatalf("spawn identity = %q, want owner", got)
	}
	// The owner opens it too while it is shared (the Chats drawer's All).
	_, oc := teamReq(t, owner, http.MethodGet, "/", nil, nil)
	if !ownsSession(oc, sess) {
		t.Fatal("the owner cannot open the recipient's chat while shared")
	}
	_, bc := teamReq(t, bob, http.MethodGet, "/", nil, nil)
	if !ownsSession(bc, sess) {
		t.Fatal("the recipient cannot open their own chat")
	}
	if r := rosterOf(t, bob); r[0].MainSessionID != chat.SessionID {
		t.Fatalf("bob's roster main chat = %q", r[0].MainSessionID)
	}
	if _, ok := mainSessionOf(owner.ID, p.ID); ok {
		t.Fatal("bob's chat became the owner's main chat")
	}

	// Unshare: gone from the roster, chat 404, old chat no longer opens.
	w, c = teamReq(t, owner, http.MethodDelete, "/", nil, map[string]string{"id": p.ID, "uid": bob.ID})
	apiTeamAgentShareRemove(c)
	if w.Code != http.StatusOK {
		t.Fatalf("unshare: %d", w.Code)
	}
	if len(rosterOf(t, bob)) != 0 {
		t.Fatal("unshared agent still in bob's roster")
	}
	w, c = teamReq(t, bob, http.MethodPost, "/", map[string]any{}, id)
	apiTeamAgentChat(c)
	if w.Code != http.StatusNotFound {
		t.Fatalf("chat after unshare: %d", w.Code)
	}
	if ownsSession(bc, sess) {
		t.Fatal("bob still opens the chat after unshare")
	}
	if ownsSession(oc, sess) {
		t.Fatal("the owner still opens bob's chat after unshare")
	}
}

// A disabled shared agent drops out of the recipient's roster.
func TestTeamAgentShareDisabledHidden(t *testing.T) {
	withTeamWorld(t)
	owner, bob := &entity.User{ID: "u1"}, &entity.User{ID: "bob"}
	p := seedTeamAgent(t, owner.ID, "helper", "")
	if code := shareWith(t, owner, p.ID, bob.ID); code != http.StatusOK {
		t.Fatalf("share: %d", code)
	}
	p.Disabled = true
	if err := globalTeam.Update(context.Background(), &p); err != nil {
		t.Fatal(err)
	}
	if len(rosterOf(t, bob)) != 0 {
		t.Fatal("disabled shared agent is listed")
	}
}

// The Captain is never shared.
func TestTeamAgentShareCaptainRefused(t *testing.T) {
	withTeamWorld(t)
	owner := &entity.User{ID: "u1"}
	cap := &entity.AgentPersona{OwnerUserID: owner.ID, Handle: "captain", IsCaptain: true, AllowedConnectors: "[]"}
	if err := globalTeam.Create(context.Background(), cap); err != nil {
		t.Fatal(err)
	}
	if code := shareWith(t, owner, cap.ID, "bob"); code != http.StatusBadRequest {
		t.Fatalf("share captain: %d, want 400", code)
	}
	w, c := teamReq(t, owner, http.MethodGet, "/", nil, map[string]string{"id": cap.ID})
	apiTeamAgentShares(c)
	var out struct {
		Shareable bool   `json:"shareable"`
		Reason    string `json:"reason"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	if out.Shareable || out.Reason == "" {
		t.Fatalf("captain shares = %s", w.Body)
	}
}

// A remote agent with no usage settings reads as only_me: not shareable.
func TestTeamAgentShareOnlyMeRemoteRefused(t *testing.T) {
	withTeamWorld(t)
	owner := &entity.User{ID: "u1"}
	p := &entity.AgentPersona{OwnerUserID: owner.ID, Handle: "far", Kind: "a2a-remote", AllowedConnectors: "[]"}
	if err := globalTeam.Create(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if !IsRemoteAgent(*p) {
		t.Skip("kind is not recognised as remote")
	}
	if code := shareWith(t, owner, p.ID, "bob"); code != http.StatusBadRequest {
		t.Fatalf("share only_me remote: %d, want 400", code)
	}
}

// A group of the recipient's may hold the shared agent.
func TestTeamSharedAgentInRecipientPeers(t *testing.T) {
	withTeamWorld(t)
	owner, bob := &entity.User{ID: "u1"}, &entity.User{ID: "bob"}
	p := seedTeamAgent(t, owner.ID, "helper", "")
	if code := shareWith(t, owner, p.ID, bob.ID); code != http.StatusOK {
		t.Fatalf("share: %d", code)
	}
	byID, _, err := ownerPeers(context.Background(), bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	if peer, ok := byID[p.ID]; !ok || peer.ChatUser != bob.ID {
		t.Fatalf("bob's peers = %+v", byID)
	}
	if got := chatUserOf(byID[p.ID]); got != bob.ID {
		t.Fatalf("chat user = %q", got)
	}
}
