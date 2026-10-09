package agents

import (
	"net/http"
	"testing"

	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/internal/entity"
)

// A recipient reading the owner's chat through a share (history on) may
// not move it onto or off one of their own tickets: that rewrites the
// owner's chat. Their own chat with the agent still attaches.
func TestTicketAttachRefusesSharedReadOnlyChat(t *testing.T) {
	withTeamWorld(t)
	owner, bob := &entity.User{ID: "u1"}, &entity.User{ID: "bob"}
	seedTeamProject(t, "p1", owner.ID)
	seedTeamProject(t, "p2", bob.ID)
	p := seedTeamAgent(t, owner.ID, "helper", "p1")
	ownChat := openChat(t, owner, p.ID, false)
	if code := shareWith(t, owner, p.ID, bob.ID); code != http.StatusOK {
		t.Fatalf("share: %d", code)
	}
	bobChat := openChat(t, bob, p.ID, false)
	if code := sessionGet(t, bob, apiSessionMeta, ownChat); code != http.StatusOK {
		t.Fatalf("bob reads the owner's chat (history on): %d", code)
	}
	tk, err := ticket.Create(globalLayout, ticket.CreateOptions{ProjectID: "p2", Title: "bob's"})
	if err != nil {
		t.Fatal(err)
	}
	vals := func(sid string) map[string]string { return map[string]string{"ticketID": tk.ID, "sid": sid} }

	w, c := teamReq(t, bob, http.MethodPut, "/", nil, vals(ownChat))
	apiTicketAttachSession(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("attach the owner's chat: %d %s, want 403", w.Code, w.Body)
	}
	if s, _ := globalMgr.Registry().Session(ownChat); s.Meta.TicketID != "" {
		t.Fatalf("owner's chat ticket pointer rewritten: %q", s.Meta.TicketID)
	}
	w, c = teamReq(t, bob, http.MethodDelete, "/", nil, vals(ownChat))
	apiTicketDetachSession(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("detach the owner's chat: %d, want 403", w.Code)
	}
	w, c = teamReq(t, bob, http.MethodPost, "/", map[string]any{"title": "x", "session_id": ownChat}, map[string]string{"id": "p2"})
	apiTicketCreate(c)
	if w.Code != http.StatusForbidden {
		t.Fatalf("create a ticket from the owner's chat: %d, want 403", w.Code)
	}

	w, c = teamReq(t, bob, http.MethodPut, "/", nil, vals(bobChat))
	apiTicketAttachSession(c)
	if w.Code != http.StatusOK {
		t.Fatalf("attach own chat: %d %s", w.Code, w.Body)
	}
}
