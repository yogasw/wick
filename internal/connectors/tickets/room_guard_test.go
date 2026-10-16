package tickets

import (
	"context"
	"errors"
	"testing"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/agents/ticket"
)

// The room itself counts as in the room, and never takes a run from
// outside (RefuseRoomSession), nor does a member's backing session.
func TestRoomItselfIsInTheRoom(t *testing.T) {
	_, l := newTestHandlers(t)
	tk, err := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "Room ticket"})
	if err != nil {
		t.Fatal(err)
	}
	room, backing := seedRoom(t, l, tk)
	if _, err := session.Create(context.Background(), l, session.CreateOptions{ID: "sub-2", ProjectID: "p1", UserID: "alice", ParentSessionID: backing}); err != nil {
		t.Fatal(err)
	}
	mkSession(t, l, "chat-1", "p1")
	for _, sid := range []string{room, backing, "sub-2"} {
		if !team.InTicketRoom(l, sid) {
			t.Fatalf("%s not in the room", sid)
		}
	}
	if team.InTicketRoom(l, "chat-1") {
		t.Fatal("an ordinary chat is in a room")
	}
	for _, sid := range []string{room, backing} {
		if err := team.RefuseRoomSession(l, sid); !errors.Is(err, team.ErrRoomSession) {
			t.Fatalf("%s takes a run from outside: %v", sid, err)
		}
	}
	if err := team.CheckProjectTarget(context.Background(), l, "alice", room, "p1"); err == nil {
		t.Fatal("the room reaches its whole project")
	}
	if !team.UnderSession(l, "sub-2", backing) || team.UnderSession(l, "chat-1", backing) {
		t.Fatal("UnderSession")
	}
}

// A ticket id is one path segment: "../" (or an encoded separator, which
// arrives decoded) never names another project's ticket.
func TestTicketIDCannotTraverse(t *testing.T) {
	_, l := newTestHandlers(t)
	tk, err := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "Mine"})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../p1/tickets/" + tk.ID, "..", ".hidden", "a/b", `a\b`, "a%2Fb", "x\x00"} {
		if ticket.ValidID(id) {
			t.Fatalf("%q is valid", id)
		}
		if ticket.Exists(l, "p1", id) {
			t.Fatalf("%q exists", id)
		}
		if _, err := ticket.Load(l, "p1", id); err == nil {
			t.Fatalf("%q loaded", id)
		}
	}
	if ticket.Exists(l, "../projects/p1", tk.ID) {
		t.Fatal("project id traversed")
	}
	if !ticket.ValidID(tk.ID) || !ticket.Exists(l, "p1", tk.ID) {
		t.Fatal("a real ticket id refused")
	}
}
