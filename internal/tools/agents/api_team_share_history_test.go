package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/yogasw/wick/internal/agents/a2aremote"
	"github.com/yogasw/wick/internal/agents/remote/slackremote"
	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// sessionGet runs one of the chat's read endpoints behind the session
// middlewares, as the router does.
func sessionGet(t *testing.T, u *entity.User, h tool.HandlerFunc, sessionID string) int {
	t.Helper()
	w, c := teamReq(t, u, http.MethodGet, "/", nil, map[string]string{"id": sessionID})
	sessionAccessMW(sharedChatReadOnlyMW(h))(c)
	return w.Code
}

// sessionPost is a change to the chat (send, approve, stop, …) behind
// the same middlewares; the handler only answers 200.
func sessionPost(t *testing.T, u *entity.User, sessionID string) int {
	t.Helper()
	w, c := teamReq(t, u, http.MethodPost, "/", map[string]any{}, map[string]string{"id": sessionID})
	sessionAccessMW(sharedChatReadOnlyMW(func(c *tool.Ctx) { c.JSON(http.StatusOK, map[string]string{"status": "ok"}) }))(c)
	return w.Code
}

func setShareHistory(t *testing.T, u *entity.User, agentID, uid string, on bool) int {
	t.Helper()
	w, c := teamReq(t, u, http.MethodPatch, "/", map[string]any{"history_visible": on}, map[string]string{"id": agentID, "uid": uid})
	apiTeamAgentShareUpdate(c)
	return w.Code
}

// A recipient opening a shared agent: their own chat's conversation,
// meta and context answer 200 (they used to 404, the chat living in the
// owner's project). With history on, the owner's chat reads 200 but takes
// no change; with history off it is refused and not listed.
func TestSharedAgentChatHistoryToggle(t *testing.T) {
	withTeamWorld(t)
	owner, bob := &entity.User{ID: "u1"}, &entity.User{ID: "bob"}
	seedTeamProject(t, "p1", owner.ID)
	p := seedTeamAgent(t, owner.ID, "helper", "p1")
	ownChat := openChat(t, owner, p.ID, false)
	if code := shareWith(t, owner, p.ID, bob.ID); code != http.StatusOK {
		t.Fatalf("share: %d", code)
	}
	bobChat := openChat(t, bob, p.ID, false)
	reads := map[string]tool.HandlerFunc{"conversation": apiSessionConversation, "meta": apiSessionMeta, "context": apiSessionContext}

	// Built-in agent, no stored choice: history on.
	for name, h := range reads {
		if code := sessionGet(t, bob, h, bobChat); code != http.StatusOK {
			t.Fatalf("own chat %s: %d", name, code)
		}
		if code := sessionGet(t, bob, h, ownChat); code != http.StatusOK {
			t.Fatalf("owner's chat %s (history on): %d", name, code)
		}
	}
	if code := sessionPost(t, bob, ownChat); code != http.StatusForbidden {
		t.Fatalf("send into the owner's chat: %d, want 403", code)
	}
	if code := sessionPost(t, bob, bobChat); code != http.StatusOK {
		t.Fatalf("send into own chat: %d", code)
	}
	if code := sessionPost(t, owner, ownChat); code != http.StatusOK {
		t.Fatalf("owner sends into own chat: %d", code)
	}
	w, c := teamReq(t, bob, http.MethodGet, "/", nil, map[string]string{"id": ownChat})
	apiSessionMeta(c)
	var meta SessionMetaDTO
	_ = json.Unmarshal(w.Body.Bytes(), &meta)
	if !meta.ReadOnly {
		t.Fatalf("owner's chat meta for bob: read_only not set: %s", w.Body)
	}
	code, r := sessionsAll(t, bob, p.ID)
	if code != http.StatusOK || !r.Shared || len(r.Sessions) != 2 {
		t.Fatalf("bob all (history on): %d %+v", code, r)
	}

	// Off: the owner's chat is refused and unlisted; bob's own still works.
	if code := setShareHistory(t, owner, p.ID, bob.ID, false); code != http.StatusOK {
		t.Fatalf("toggle off: %d", code)
	}
	for name, h := range reads {
		if code := sessionGet(t, bob, h, ownChat); code != http.StatusNotFound {
			t.Fatalf("owner's chat %s (history off): %d, want 404", name, code)
		}
		if code := sessionGet(t, bob, h, bobChat); code != http.StatusOK {
			t.Fatalf("own chat %s (history off): %d", name, code)
		}
	}
	if code := sessionPost(t, bob, bobChat); code != http.StatusOK {
		t.Fatalf("send into own chat (history off): %d", code)
	}
	code, r = sessionsAll(t, bob, p.ID)
	if code != http.StatusOK || r.Shared || len(r.Sessions) != 1 || r.Sessions[0].ID != bobChat {
		t.Fatalf("bob all (history off): %d %+v", code, r)
	}
	ownSess, _ := globalMgr.Registry().Session(ownChat)
	if sharedAgentChatVisible(context.Background(), bob.ID, ownSess) {
		t.Fatal("live status leaks the owner's chat with history off")
	}
	// The owner still sees bob's chat: the toggle is about recipients.
	code, r = sessionsAll(t, owner, p.ID)
	if code != http.StatusOK || !r.Shared || len(r.Sessions) != 2 {
		t.Fatalf("owner all: %d %+v", code, r)
	}
	// Stored choice shows in the share list.
	w, c = teamReq(t, owner, http.MethodGet, "/", nil, map[string]string{"id": p.ID})
	apiTeamAgentShares(c)
	var list struct {
		Shares         []teamShareItem `json:"shares"`
		HistoryDefault bool            `json:"history_default"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Shares) != 1 || list.Shares[0].HistoryVisible || !list.HistoryDefault {
		t.Fatalf("shares: %s", w.Body)
	}
	if code := setShareHistory(t, bob, p.ID, bob.ID, true); code == http.StatusOK {
		t.Fatal("a recipient flipped the owner's toggle")
	}
}

// The default follows the agent type: on for built-in, off for remote;
// a stored value wins.
func TestShareHistoryDefaults(t *testing.T) {
	on, off := true, false
	local := entity.AgentPersona{}
	slack := entity.AgentPersona{Kind: slackremote.Kind}
	if !shareHistoryVisible(local, entity.AgentShare{}) {
		t.Fatal("built-in default should be on")
	}
	if shareHistoryVisible(slack, entity.AgentShare{}) {
		t.Fatal("remote default should be off")
	}
	if shareHistoryVisible(local, entity.AgentShare{HistoryVisible: &off}) || !shareHistoryVisible(slack, entity.AgentShare{HistoryVisible: &on}) {
		t.Fatal("stored value must win over the default")
	}
	if shareRemoteKind(slack) != "slack" || shareRemoteKind(local) != "" {
		t.Fatal("remote kind")
	}
}

// A remote agent's conversation (Slack thread, A2A context id) lives in
// the chat's own session folder, so every new chat P6 opens for a new
// caller session starts a new remote conversation: nothing of the
// previous chat's thread or context is carried over.
func TestNewChatStartsNewRemoteConversation(t *testing.T) {
	withTeamWorld(t)
	owner := &entity.User{ID: "u1"}
	seedTeamProject(t, "p1", owner.ID)
	p := seedTeamAgent(t, owner.ID, "remote", "p1")
	openChat(t, owner, p.ID, false)
	ctx := context.Background()
	turns := poolTurns{}
	peer := teamlink.Peer{ID: p.ID, OwnerID: owner.ID, Handle: "remote"}

	first, err := turns.NewChat(ctx, peer)
	if err != nil {
		t.Fatal(err)
	}
	dir := globalLayout.SessionDir(first)
	_ = os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(filepath.Join(dir, "slack-remote.json"), []byte(`{"channel":"C1","thread_ts":"1.1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a2a-remote.json"), []byte(`{"context_id":"ctx-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if slackremote.LoadState(dir).ThreadTS != "1.1" || a2aremote.LoadState(dir).ContextID != "ctx-1" {
		t.Fatal("state not written where the remote sources read it")
	}
	if err := turns.Link(ctx, peer, first, "caller-a"); err != nil {
		t.Fatal(err)
	}
	second, err := turns.NewChat(ctx, peer)
	if err != nil || second == first {
		t.Fatalf("second chat = %q, %v", second, err)
	}
	if err := turns.Link(ctx, peer, second, "caller-b"); err != nil {
		t.Fatal(err)
	}
	dir2 := globalLayout.SessionDir(second)
	if st := slackremote.LoadState(dir2); st.ThreadTS != "" {
		t.Fatalf("new chat reuses Slack thread %q", st.ThreadTS)
	}
	if st := a2aremote.LoadState(dir2); st.ContextID != "" {
		t.Fatalf("new chat reuses A2A context %q", st.ContextID)
	}
	if got := turns.LinkedChat(ctx, peer, "caller-a"); got != first {
		t.Fatalf("caller-a pair = %q, want %q", got, first)
	}
	if got := turns.LinkedChat(ctx, peer, "caller-b"); got != second {
		t.Fatalf("caller-b pair = %q, want %q", got, second)
	}
}
