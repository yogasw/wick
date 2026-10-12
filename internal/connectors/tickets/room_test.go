package tickets

import (
	"context"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/ticket"
)

// seedRoom lays down a ticket room on tk in p1 with the project default as
// its home, and a member backing session for alice (project default: no
// agent), in the same project as the room — the case where "the calling
// session's own project" must not open the rest of the project.
func seedRoom(t *testing.T, l agentconfig.Layout, tk ticket.Ticket) (room, backing string) {
	t.Helper()
	room, backing = "room-1", "backing-1"
	mkSession(t, l, room, "p1")
	s, err := session.Load(l, room)
	if err != nil {
		t.Fatal(err)
	}
	s.Meta.TicketRoom = &session.TicketRoom{TicketID: tk.ID, Members: []session.RoomMember{{Kind: session.RoomMemberProjectDefault, Home: true}}}
	if err := session.SaveMeta(l, room, s.Meta); err != nil {
		t.Fatal(err)
	}
	if err := ticket.AttachSession(l, "p1", tk.ID, room); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Create(context.Background(), l, session.CreateOptions{ID: backing, ProjectID: "p1", UserID: "alice"}); err != nil {
		t.Fatal(err)
	}
	b, _ := session.Load(l, backing)
	b.Meta.GroupSessionID = room
	if err := session.SaveMeta(l, backing, b.Meta); err != nil {
		t.Fatal(err)
	}
	return room, backing
}

// A room member — the project default included — reaches its room's ticket
// and nothing else of the project, nor does a sub-agent it spawns.
func TestRoomMemberReachesOnlyItsTicket(t *testing.T) {
	h, l := newTestHandlers(t)
	mine, err := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "Room ticket"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "Other ticket"})
	if err != nil {
		t.Fatal(err)
	}
	_, backing := seedRoom(t, l, mine)

	got := mustDispatch(t, h.get, ctxFor(backing, map[string]string{}))
	if v, ok := got.(ticketView); !ok || v.ID != mine.ID {
		t.Fatalf("implicit ticket: %#v", got)
	}
	mustDispatch(t, h.get, ctxFor(backing, map[string]string{"ticket_id": mine.ID, "project_id": "p1"}))
	if _, err := h.get(ctxFor(backing, map[string]string{"ticket_id": other.ID})); err == nil {
		t.Fatal("read another ticket of the project")
	}
	if _, err := h.get(ctxFor(backing, map[string]string{"ticket_id": other.ID, "project_id": "p1"})); err == nil {
		t.Fatal("read another ticket with the project named")
	}
	if _, err := h.update(ctxFor(backing, map[string]string{"ticket_id": other.ID, "status": "done"})); err == nil {
		t.Fatal("updated another ticket of the project")
	}
	if _, err := h.list(ctxFor(backing, map[string]string{})); err == nil {
		t.Fatal("listed the project's tickets")
	}
	if _, err := h.list(ctxFor(backing, map[string]string{"project_id": "p1"})); err == nil {
		t.Fatal("listed the project's tickets with the project named")
	}
	// A sub-agent of the member reaches nothing of the project either.
	if _, err := session.Create(context.Background(), l, session.CreateOptions{ID: "sub-1", ProjectID: "p1", UserID: "alice", ParentSessionID: backing}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.get(ctxFor("sub-1", map[string]string{"ticket_id": other.ID})); err == nil {
		t.Fatal("sub-agent read another ticket")
	}
	if _, err := h.list(ctxFor("sub-1", map[string]string{})); err == nil {
		t.Fatal("sub-agent listed the project's tickets")
	}
	// An ordinary chat of the project is unaffected.
	mkSession(t, l, "chat-1", "p1")
	mustDispatch(t, h.get, ctxFor("chat-1", map[string]string{"ticket_id": other.ID}))
}
