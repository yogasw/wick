package tickets

import (
	"context"
	"errors"
	"fmt"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/team"
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

// tickets.attach / tickets.detach never move a room off its ticket, nor
// file a member's backing session — even for an ordinary chat of the same
// project that may otherwise reach both sessions.
func TestRoomSessionCannotBeMovedOrDetached(t *testing.T) {
	h, l := newTestHandlers(t)
	mine, err := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "Room ticket"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "Other ticket"})
	if err != nil {
		t.Fatal(err)
	}
	room, backing := seedRoom(t, l, mine)
	mkSession(t, l, "chat-1", "p1")

	for _, sid := range []string{room, backing} {
		if _, err := h.attach(ctxFor("chat-1", map[string]string{"ticket_id": other.ID, "session_id": sid})); !errors.Is(err, team.ErrRoomSession) {
			t.Fatalf("attach %s: err = %v", sid, err)
		}
	}
	if _, err := h.detach(ctxFor("chat-1", map[string]string{"ticket_id": mine.ID, "session_id": room})); !errors.Is(err, team.ErrRoomSession) {
		t.Fatalf("detach room: err = %v", err)
	}
	tk, _ := ticket.Load(l, "p1", mine.ID)
	if len(tk.Sessions) != 1 || tk.Sessions[0] != room {
		t.Fatalf("room ticket sessions = %v", tk.Sessions)
	}
	if s, _ := session.Load(l, room); s.Meta.TicketID != "" && s.Meta.TicketID != mine.ID {
		t.Fatalf("room back-pointer moved to %q", s.Meta.TicketID)
	}
	// An ordinary chat still files as before.
	mustDispatch(t, h.attach, ctxFor("chat-1", map[string]string{"ticket_id": other.ID}))

	// tickets.create cannot seed a new ticket with a room session either.
	before, _ := ticket.List(l, "p1")
	for _, sid := range []string{room, backing} {
		if _, err := h.create(ctxFor(sid, map[string]string{"title": "From the room", "attach_current_session": "true", "project_id": "p1"})); err == nil {
			t.Fatalf("create from %s attached it", sid)
		}
	}
	if after, _ := ticket.List(l, "p1"); len(after) != len(before) {
		t.Fatalf("create from a room session made a ticket: %d -> %d", len(before), len(after))
	}

	// An ordinary group chat's backing session is not a room's: it files.
	mkSession(t, l, "group-1", "p1")
	if _, err := session.Create(context.Background(), l, session.CreateOptions{ID: "gbacking-1", ProjectID: "p1", UserID: "alice"}); err != nil {
		t.Fatal(err)
	}
	gb, _ := session.Load(l, "gbacking-1")
	gb.Meta.GroupSessionID = "group-1"
	if err := session.SaveMeta(l, "gbacking-1", gb.Meta); err != nil {
		t.Fatal(err)
	}
	if err := team.RefuseRoomSession(l, "gbacking-1"); err != nil {
		t.Fatalf("group backing refused: %v", err)
	}
	mustDispatch(t, h.attach, ctxFor("chat-1", map[string]string{"ticket_id": other.ID, "session_id": "gbacking-1"}))
}

// RoomTicket grants nothing once the ticket no longer lists the room, or
// the ticket is gone, whatever the room's own meta still says.
func TestRoomTicketNeedsTheTicketAndTheRoomOnIt(t *testing.T) {
	_, l := newTestHandlers(t)
	mine, err := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "Room ticket"})
	if err != nil {
		t.Fatal(err)
	}
	room, backing := seedRoom(t, l, mine)
	ctx := context.Background()
	if _, tid, ok := team.RoomTicket(ctx, l, backing); !ok || tid != mine.ID {
		t.Fatalf("member: %q %v", tid, ok)
	}
	if err := ticket.DetachSession(l, "p1", mine.ID, room); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := team.RoomTicket(ctx, l, backing); ok {
		t.Fatal("room off its ticket still granted the ticket")
	}
	if err := ticket.AttachSession(l, "p1", mine.ID, room); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := team.RoomTicket(ctx, l, backing); !ok {
		t.Fatal("room back on its ticket")
	}
	if err := ticket.Delete(l, "p1", mine.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := team.RoomTicket(ctx, l, backing); ok {
		t.Fatal("deleted ticket still granted")
	}
}

// A deep chain of ordinary sub-agents is not a room: every level keeps its
// project. The same depth under a room member is still in the room.
func TestDeepSubAgentChainIsNotARoom(t *testing.T) {
	h, l := newTestHandlers(t)
	other, err := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "Other ticket"})
	if err != nil {
		t.Fatal(err)
	}
	chain := func(prefix, root string, n int) string {
		parent := root
		for i := 0; i < n; i++ {
			id := fmt.Sprintf("%s-%d", prefix, i)
			if _, err := session.Create(context.Background(), l, session.CreateOptions{ID: id, ProjectID: "p1", UserID: "alice", ParentSessionID: parent}); err != nil {
				t.Fatal(err)
			}
			parent = id
		}
		return parent
	}
	mkSession(t, l, "top", "p1")
	leaf := chain("deep", "top", 20)
	if team.InTicketRoom(l, leaf) {
		t.Fatal("a 20-deep ordinary chain read as a room")
	}
	mustDispatch(t, h.get, ctxFor(leaf, map[string]string{"ticket_id": other.ID}))
	mustDispatch(t, h.list, ctxFor(leaf, map[string]string{}))

	mine, err := ticket.Create(l, ticket.CreateOptions{ProjectID: "p1", Title: "Room ticket"})
	if err != nil {
		t.Fatal(err)
	}
	_, backing := seedRoom(t, l, mine)
	roomLeaf := chain("roomsub", backing, 20)
	if !team.InTicketRoom(l, roomLeaf) {
		t.Fatal("a 20-deep chain under a room member left the room")
	}
	if _, err := h.get(ctxFor(roomLeaf, map[string]string{"ticket_id": other.ID})); err == nil {
		t.Fatal("deep room sub-agent read another ticket")
	}
}
