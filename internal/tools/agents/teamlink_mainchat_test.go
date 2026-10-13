package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/entity"
)

// mainChatsOf counts uid's main chats with agentID.
func mainChatsOf(uid, agentID string) []session.Session {
	var out []session.Session
	for _, s := range agentSessions(uid, agentID) {
		if s.Meta.AgentMain {
			out = append(out, s)
		}
	}
	return out
}

// sharedTeamAgent seeds an owner's agent in p1, its main chat open, and
// shares it with bob.
func sharedTeamAgent(t *testing.T) (entity.AgentPersona, teamlink.Peer) {
	t.Helper()
	withTeamWorld(t)
	owner := &entity.User{ID: "u1"}
	seedTeamProject(t, "p1", owner.ID)
	p := seedTeamAgent(t, owner.ID, "atraxa", "p1")
	openChat(t, owner, p.ID, false)
	if code := shareWith(t, owner, p.ID, "bob"); code != http.StatusOK {
		t.Fatalf("share: %d", code)
	}
	return p, teamlink.Peer{ID: p.ID, OwnerID: owner.ID, Handle: p.Handle, ChatUser: "bob"}
}

// A task to a shared agent bob never opened opens his main chat — one,
// his, in the agent's project — and the task runs there.
func TestTeamTaskOpensRecipientMainChat(t *testing.T) {
	p, peer := sharedTeamAgent(t)
	ctx := context.Background()
	if n := len(mainChatsOf("bob", p.ID)); n != 0 {
		t.Fatalf("bob starts with %d main chats", n)
	}
	if _, err := (poolTurns{}).NewChat(ctx, peer); err != nil {
		t.Fatalf("task chat: %v", err)
	}
	mains := mainChatsOf("bob", p.ID)
	if len(mains) != 1 {
		t.Fatalf("bob has %d main chats, want 1", len(mains))
	}
	m := mains[0].Meta
	if m.UserID != "bob" || m.AgentID != p.ID || m.ProjectID != "p1" || m.Origin != session.OriginUI {
		t.Fatalf("main chat meta: %+v", m)
	}
	s, err := mainChatOf(ctx, peer)
	if err != nil || s.ID != mains[0].ID {
		t.Fatalf("second task: %q %v, want %q", s.ID, err, mains[0].ID)
	}
	if n := len(mainChatsOf("u1", p.ID)); n != 1 {
		t.Fatalf("owner has %d main chats, want 1", n)
	}
}

// Tasks arriving together open one main chat between them.
func TestTeamTaskConcurrentOpensOneMainChat(t *testing.T) {
	p, peer := sharedTeamAgent(t)
	ctx := context.Background()
	const n = 8
	ids := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			s, err := mainChatOf(ctx, peer)
			ids[i], errs[i] = s.ID, err
		}(i)
	}
	wg.Wait()
	for i := range ids {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("call %d: %q %v, want %q", i, ids[i], errs[i], ids[0])
		}
	}
	if got := len(mainChatsOf("bob", p.ID)); got != 1 {
		t.Fatalf("bob has %d main chats, want 1", got)
	}
}

// Someone the agent is not shared with gets the error they always got,
// and nothing is created.
func TestTeamTaskNonMemberKeepsError(t *testing.T) {
	p, peer := sharedTeamAgent(t)
	peer.ChatUser = "carol"
	_, err := mainChatOf(context.Background(), peer)
	if err == nil || !strings.Contains(err.Error(), "has no chat with you yet") {
		t.Fatalf("err = %v", err)
	}
	if n := len(agentSessions("carol", p.ID)); n != 0 {
		t.Fatalf("carol has %d chats, want 0", n)
	}
}

// A main chat bob already opened is the one a task uses.
func TestTeamTaskReusesRecipientMainChat(t *testing.T) {
	p, peer := sharedTeamAgent(t)
	own := openChat(t, &entity.User{ID: "bob"}, p.ID, false)
	s, err := mainChatOf(context.Background(), peer)
	if err != nil || s.ID != own {
		t.Fatalf("main chat = %q %v, want %q", s.ID, err, own)
	}
	if n := len(agentSessions("bob", p.ID)); n != 1 {
		t.Fatalf("bob has %d chats, want 1", n)
	}
}

// A share revoked or an agent turned off: the task gets the error and
// no chat is opened.
func TestTeamTaskRevokedOrDisabledKeepsError(t *testing.T) {
	t.Run("revoked", func(t *testing.T) {
		p, peer := sharedTeamAgent(t)
		w, c := teamReq(t, &entity.User{ID: "u1"}, http.MethodDelete, "/", nil, map[string]string{"id": p.ID, "uid": "bob"})
		apiTeamAgentShareRemove(c)
		if w.Code != http.StatusOK {
			t.Fatalf("unshare: %d", w.Code)
		}
		if _, err := mainChatOf(context.Background(), peer); err == nil || !strings.Contains(err.Error(), "has no chat with you yet") {
			t.Fatalf("err = %v", err)
		}
		if n := len(agentSessions("bob", p.ID)); n != 0 {
			t.Fatalf("bob has %d chats, want 0", n)
		}
	})
	t.Run("disabled", func(t *testing.T) {
		p, peer := sharedTeamAgent(t)
		p.Disabled = true
		if err := globalTeam.Update(context.Background(), &p); err != nil {
			t.Fatal(err)
		}
		if _, err := mainChatOf(context.Background(), peer); err == nil || !strings.Contains(err.Error(), "has no chat with you yet") {
			t.Fatalf("err = %v", err)
		}
		if n := len(agentSessions("bob", p.ID)); n != 0 {
			t.Fatalf("bob has %d chats, want 0", n)
		}
	})
}

// The owner named as ChatUser keeps the owner's error; nothing opened.
func TestTeamTaskOwnerChatUserKeepsOwnerError(t *testing.T) {
	withTeamWorld(t)
	seedTeamProject(t, "p1", "u1")
	p := seedTeamAgent(t, "u1", "atraxa", "p1")
	peer := teamlink.Peer{ID: p.ID, OwnerID: "u1", Handle: p.Handle, ChatUser: "u1"}
	if _, err := mainChatOf(context.Background(), peer); err == nil || !strings.Contains(err.Error(), "its owner has to open it") {
		t.Fatalf("err = %v", err)
	}
	if n := len(agentSessions("u1", p.ID)); n != 0 {
		t.Fatalf("owner has %d chats, want 0", n)
	}
}

// A recipient whose account is not approved gets no chat opened.
func TestTeamTaskInactiveRecipientKeepsError(t *testing.T) {
	p, peer := sharedTeamAgent(t)
	prev := lookupWorkflowOwner
	lookupWorkflowOwner = func(_ context.Context, id string) (*entity.User, error) {
		return &entity.User{ID: id, Approved: false}, nil
	}
	t.Cleanup(func() { lookupWorkflowOwner = prev })
	if _, err := mainChatOf(context.Background(), peer); err == nil || !strings.Contains(err.Error(), "has no chat with you yet") {
		t.Fatalf("err = %v", err)
	}
	if n := len(agentSessions("bob", p.ID)); n != 0 {
		t.Fatalf("bob has %d chats, want 0", n)
	}
}

// Opening the agent from the roster while tasks open the same chat: one
// main chat between them, the one every caller gets.
func TestTeamTaskRacesRosterOpen(t *testing.T) {
	p, peer := sharedTeamAgent(t)
	bob := &entity.User{ID: "bob"}
	const n = 6
	ids := make([]string, 2*n)
	errs := make([]error, 2*n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			s, err := mainChatOf(context.Background(), peer)
			ids[i], errs[i] = s.ID, err
		}(i)
		go func(i int) {
			defer wg.Done()
			w, c := teamReq(t, bob, http.MethodPost, "/", map[string]any{"new": false}, map[string]string{"id": p.ID})
			apiTeamAgentChat(c)
			var out struct {
				SessionID string `json:"session_id"`
			}
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil {
				errs[n+i] = fmt.Errorf("roster open: %d %s", w.Code, w.Body)
				return
			}
			ids[n+i] = out.SessionID
		}(i)
	}
	wg.Wait()
	for i := range ids {
		if errs[i] != nil || ids[i] != ids[0] {
			t.Fatalf("call %d: %q %v, want %q", i, ids[i], errs[i], ids[0])
		}
	}
	if got := len(mainChatsOf("bob", p.ID)); got != 1 {
		t.Fatalf("bob has %d main chats, want 1", got)
	}
}
