package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/internal/entity"
)

// openRoom creates (or returns) tk's room as u and decodes the answer.
func openRoom(t *testing.T, u *entity.User, ticketID string) (int, TicketRoomItem) {
	t.Helper()
	w, c := teamReq(t, u, http.MethodPost, "/", nil, map[string]string{"ticketID": ticketID})
	apiTicketRoomCreate(c)
	var it TicketRoomItem
	_ = json.Unmarshal(w.Body.Bytes(), &it)
	return w.Code, it
}

func addRoomMembers(t *testing.T, u *entity.User, ticketID string, ids ...string) int {
	t.Helper()
	w, c := teamReq(t, u, http.MethodPost, "/", map[string]any{"agent_ids": ids}, map[string]string{"ticketID": ticketID})
	apiTicketRoomAddMembers(c)
	return w.Code
}

func removeRoomMember(t *testing.T, u *entity.User, ticketID, agentID string) int {
	t.Helper()
	w, c := teamReq(t, u, http.MethodDelete, "/", nil, map[string]string{"ticketID": ticketID, "agentID": agentID})
	apiTicketRoomRemoveMember(c)
	return w.Code
}

func seedTicket(t *testing.T, projectID, title string) ticket.Ticket {
	t.Helper()
	tk, err := ticket.Create(globalLayout, ticket.CreateOptions{ProjectID: projectID, Title: title})
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

// The room lives in the ticket's project, is attached to the ticket, starts
// with the home member, and a second open returns the same room.
func TestTicketRoomCreatedInTicketProjectAndAttached(t *testing.T) {
	withTeamWorld(t)
	alice := &entity.User{ID: "alice"}
	seedTeamProject(t, "p1", alice.ID)
	tk := seedTicket(t, "p1", "Webhook 401")

	code, it := openRoom(t, alice, tk.ID)
	if code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	sess, ok := globalMgr.Registry().Session(it.SessionID)
	if !ok || sess.Meta.ProjectID != "p1" || sess.Meta.TicketRoom == nil || sess.Meta.TicketRoom.TicketID != tk.ID {
		t.Fatalf("room meta: %+v", sess.Meta)
	}
	if sess.Meta.UserID != alice.ID || sess.Meta.AgentID != "" {
		t.Fatalf("room owner/agent: %q %q", sess.Meta.UserID, sess.Meta.AgentID)
	}
	home, _ := sess.Meta.TicketRoom.Home()
	if home.Kind != session.RoomMemberProjectDefault || len(sess.Meta.TicketRoom.Members) != 1 {
		t.Fatalf("members: %+v", sess.Meta.TicketRoom.Members)
	}
	got, _ := ticket.Load(globalLayout, "p1", tk.ID)
	if len(got.Sessions) != 1 || got.Sessions[0] != it.SessionID {
		t.Fatalf("not attached: %v", got.Sessions)
	}
	if code, again := openRoom(t, alice, tk.ID); code != http.StatusOK || again.SessionID != it.SessionID {
		t.Fatalf("second open: %d %q, want 200 %q", code, again.SessionID, it.SessionID)
	}
	// Someone who cannot open the project cannot open its room either.
	if code, _ := openRoom(t, &entity.User{ID: "mallory"}, tk.ID); code != http.StatusNotFound {
		t.Fatalf("outsider: %d, want 404", code)
	}
}

// Only the caller's own agents join, and only the caller removes them.
func TestTicketRoomMembersAreTheCallersOwnAgents(t *testing.T) {
	withTeamWorld(t)
	alice, bob := &entity.User{ID: "alice"}, &entity.User{ID: "bob"}
	seedTeamProject(t, "p1", alice.ID)
	seedTeamProject(t, "pa", alice.ID)
	seedTeamProject(t, "pb", bob.ID)
	mine := seedTeamAgent(t, alice.ID, "helper", "pa")
	theirs := seedTeamAgent(t, bob.ID, "helper", "pb")
	tk := seedTicket(t, "p1", "Shared work")
	if code, _ := openRoom(t, alice, tk.ID); code != http.StatusCreated {
		t.Fatalf("create: %d", code)
	}
	if code := addRoomMembers(t, alice, tk.ID, theirs.ID); code != http.StatusBadRequest {
		t.Fatalf("add bob's agent as alice: %d, want 400", code)
	}
	if code := addRoomMembers(t, alice, tk.ID, mine.ID); code != http.StatusOK {
		t.Fatalf("add own agent: %d", code)
	}
	// Bob cannot open the project, so he cannot touch its room.
	if code := addRoomMembers(t, bob, tk.ID, theirs.ID); code != http.StatusNotFound {
		t.Fatalf("bob without the project: %d, want 404", code)
	}
	room, _ := findTicketRoom("p1", tk.ID)
	if _, ok := room.Meta.TicketRoom.Member(mine.ID, alice.ID); !ok {
		t.Fatal("alice's agent missing")
	}
	// Bob's agent in the room (brought in while he had access): alice
	// cannot remove it.
	if err := saveGroupMeta(room.ID, func(m *session.Meta) {
		m.TicketRoom.Members = append(m.TicketRoom.Members, session.RoomMember{Kind: session.RoomMemberAgent, AgentID: theirs.ID, OwnerID: bob.ID})
	}); err != nil {
		t.Fatal(err)
	}
	if code := removeRoomMember(t, alice, tk.ID, theirs.ID); code != http.StatusBadRequest {
		t.Fatalf("alice removes bob's agent: %d, want 400", code)
	}
	if code := removeRoomMember(t, alice, tk.ID, mine.ID); code != http.StatusOK {
		t.Fatalf("alice removes her own: %d", code)
	}
}

// Someone else's agent never runs on your message: "@hana/captain" and a
// handle that is none of yours plan no target; your own handle joins and
// runs; bob's members are never among alice's peers.
func TestTicketRoomPlanRunsOnlyTheSendersAgents(t *testing.T) {
	home := teamlink.Peer{ID: roomHomePeerID, Handle: roomHomeHandle}
	aliceCap := teamlink.Peer{ID: "a-cap", OwnerID: "alice", Handle: "captain", IsCaptain: true}
	aliceLog := teamlink.Peer{ID: "a-log", OwnerID: "alice", Handle: "log-hunter"}
	tr := session.TicketRoom{TicketID: "T-1", Members: []session.RoomMember{
		{Kind: session.RoomMemberProjectDefault, Home: true},
		{Kind: session.RoomMemberAgent, AgentID: "b-cap", OwnerID: "bob"},
	}}
	own := []teamlink.Peer{aliceCap, aliceLog}

	plan, err := planRoomMessage("@bob/captain please check", tr, "alice", own, home)
	if err != nil || len(plan.Targets) != 0 || len(plan.Foreign) != 1 || plan.Foreign[0].Owner != "bob" {
		t.Fatalf("foreign mention: %+v %v", plan, err)
	}
	plan, _ = planRoomMessage("@someone look", tr, "alice", own, home)
	if len(plan.Targets) != 0 || len(plan.Unknown) != 1 {
		t.Fatalf("unknown handle: %+v", plan)
	}
	plan, _ = planRoomMessage("@log-hunter grep the 401s", tr, "alice", own, home)
	if len(plan.Join) != 1 || plan.Join[0] != "a-log" || len(plan.Targets) != 1 || plan.Targets[0].ID != "a-log" {
		t.Fatalf("own mention: %+v", plan)
	}
	for _, p := range plan.Peers {
		if p.ID == "b-cap" {
			t.Fatal("bob's agent is among alice's peers")
		}
	}
	if len(tr.Members) != 2 {
		t.Fatalf("plan changed the room: %+v", tr.Members)
	}
	// No @: the project default (alice's Captain is not a member yet).
	plan, _ = planRoomMessage("what is the status?", tr, "alice", own, home)
	if len(plan.Targets) != 1 || plan.Targets[0].ID != roomHomePeerID {
		t.Fatalf("default: %+v", plan.Targets)
	}
	// Once her Captain is in, no @ goes to it.
	tr.Members = append(tr.Members, session.RoomMember{Kind: session.RoomMemberAgent, AgentID: "a-cap", OwnerID: "alice"})
	plan, _ = planRoomMessage("what is the status?", tr, "alice", own, home)
	if len(plan.Targets) != 1 || plan.Targets[0].ID != "a-cap" {
		t.Fatalf("captain default: %+v", plan.Targets)
	}
	// Bob's Captain answers bob only: alice's message never reaches it,
	// and bob with no Captain in the room gets the home member.
	plan, _ = planRoomMessage("status?", tr, "bob", []teamlink.Peer{{ID: "b-cap", OwnerID: "bob", Handle: "captain", IsCaptain: true}}, home)
	if len(plan.Targets) != 1 || plan.Targets[0].ID != "b-cap" {
		t.Fatalf("bob's default: %+v", plan.Targets)
	}
	// A home agent that is someone else's takes nobody else's message.
	tr2 := session.TicketRoom{Members: []session.RoomMember{{Kind: session.RoomMemberAgent, AgentID: "b-home", OwnerID: "bob", Home: true}}}
	plan, _ = planRoomMessage("hello", tr2, "alice", nil, home)
	if len(plan.Targets) != 0 || !plan.NoDefault {
		t.Fatalf("someone else's home agent: %+v", plan)
	}
}

// A member reads only its person's part of the thread unless that person
// shares the full thread; then others are named "handle (@owner)".
func TestTicketRoomPromptIsPerPerson(t *testing.T) {
	owner := map[string]string{"s-alice": "alice", "s-bob": "bob"}
	hist := []store.ConversationTurn{
		{Role: "user", Text: "alice asks", Sender: &store.Sender{ID: "alice", WickUserID: "alice", Name: "Alice"}},
		{Role: "user", Text: "bob secret", Sender: &store.Sender{ID: "bob", WickUserID: "bob", Name: "Bob"}},
		{Role: "assistant", Text: "bob's captain answer", Speaker: &store.Speaker{AgentID: "b-cap", Handle: "captain", SessionID: "s-bob"}},
		{Role: "assistant", Text: "alice's helper answer", Speaker: &store.Speaker{AgentID: "a-help", Handle: "helper", SessionID: "s-alice"}},
		{Role: "system", Text: "@helper joined the room"},
	}
	in := roomPromptIn{
		Ticket:  ticket.Ticket{ID: "T-1", Title: "Webhook", Status: "open", Body: "It fails."},
		Self:    teamlink.Peer{ID: "a-help", Handle: "helper"},
		Sender:  "alice",
		History: hist,
		OwnerOf: func(t store.ConversationTurn) string {
			if t.Speaker != nil {
				return owner[t.Speaker.SessionID]
			}
			if t.Sender != nil {
				return t.Sender.WickUserID
			}
			return ""
		},
		HandleOf: func(uid string) string { return uid },
	}
	p := roomPrompt(in)
	for _, want := range []string{"alice asks", "alice's helper answer", "joined the room", "T-1", "It fails.", "@helper (agent a-help)"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, p)
		}
	}
	for _, not := range []string{"bob secret", "bob's captain answer"} {
		if strings.Contains(p, not) {
			t.Fatalf("prompt leaks %q:\n%s", not, p)
		}
	}
	in.ShareFull = true
	p = roomPrompt(in)
	if !strings.Contains(p, "bob secret") || !strings.Contains(p, "captain (@bob)") || !strings.Contains(p, "Bob (@bob)") {
		t.Fatalf("shared thread:\n%s", p)
	}
}

// A member's backing session reaches the room's ticket only while it is a
// member and its person may open the project.
func TestTicketRoomDataScope(t *testing.T) {
	withTeamWorld(t)
	alice := &entity.User{ID: "alice"}
	seedTeamProject(t, "p1", alice.ID)
	seedTeamProject(t, "pa", alice.ID)
	helper := seedTeamAgent(t, alice.ID, "helper", "pa")
	tk := seedTicket(t, "p1", "Room ticket")
	other := seedTicket(t, "p1", "Other ticket")
	_, it := openRoom(t, alice, tk.ID)
	if code := addRoomMembers(t, alice, tk.ID, helper.ID); code != http.StatusOK {
		t.Fatalf("add: %d", code)
	}
	_, c := teamReq(t, alice, http.MethodPost, "/", nil, nil)
	backing, err := groupBacking(c, it.SessionID, helper.ID)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if p, tid, ok := team.RoomTicket(ctx, globalLayout, backing); !ok || p != "p1" || tid != tk.ID || tid == other.ID {
		t.Fatalf("member: %q %q %v", p, tid, ok)
	}
	// An ordinary chat of the same agent reaches nothing through the room.
	chat := openChat(t, alice, helper.ID, false)
	if _, _, ok := team.RoomTicket(ctx, globalLayout, chat); ok {
		t.Fatal("a chat outside the room got the room's ticket")
	}
	// Access follows the person: losing the project ends it.
	pm, err := project.Load(globalLayout, "p1")
	if err != nil {
		t.Fatal(err)
	}
	meta := pm.Meta
	meta.OwnerUserID = "someone-else"
	if err := project.SaveMeta(globalLayout, "p1", meta); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := team.RoomTicket(ctx, globalLayout, backing); ok {
		t.Fatal("still reaches the ticket after losing the project")
	}
	pm.Meta.OwnerUserID = alice.ID
	if err := project.SaveMeta(globalLayout, "p1", pm.Meta); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := team.RoomTicket(ctx, globalLayout, backing); !ok {
		t.Fatal("access back: should reach it again")
	}
	// Removal ends it at once.
	if code := removeRoomMember(t, alice, tk.ID, helper.ID); code != http.StatusOK {
		t.Fatalf("remove: %d", code)
	}
	if _, _, ok := team.RoomTicket(ctx, globalLayout, backing); ok {
		t.Fatal("still reaches the ticket after removal")
	}
}

// Agents handing each other turns stop at the room's cap: the smallest
// max_hops of the sender's members.
func TestTicketRoomHopCap(t *testing.T) {
	a := teamlink.Peer{ID: "a", OwnerID: "alice", Handle: "ann", MaxHops: 2}
	b := teamlink.Peer{ID: "b", OwnerID: "alice", Handle: "ben", MaxHops: 5}
	peers := []teamlink.Peer{a, b}
	var mu sync.Mutex
	turns := 0
	var recorded []store.ConversationTurn
	run := &groupRun{
		members: peers, limit: teamlink.GroupLimit(peers, 0), backing: map[string]string{},
		turn: func(_ context.Context, p teamlink.Peer, _ string) (string, error) {
			mu.Lock()
			turns++
			mu.Unlock()
			if p.ID == "a" {
				return "@ben your turn", nil
			}
			return "@ann your turn", nil
		},
		history: func() []store.ConversationTurn { return nil },
		record:  func(t store.ConversationTurn) { recorded = append(recorded, t) },
		prompt:  func(teamlink.Peer, string, []store.ConversationTurn) string { return "" },
	}
	run.play(context.Background(), "room-hop-test", []teamlink.Peer{a})
	if run.limit != 2 || turns != 3 {
		t.Fatalf("limit %d, turns %d; want 2 and 3 (one person turn + 2 hops)", run.limit, turns)
	}
	last := recorded[len(recorded)-1]
	if last.Role != "system" {
		t.Fatalf("no hop-limit line: %+v", last)
	}
}

// A board card carries its room's members (for avatars and the "Agent in
// room" filter); the room itself is not listed among the card's chats.
func TestTicketBoardCardCarriesRoom(t *testing.T) {
	withTeamWorld(t)
	alice := &entity.User{ID: "alice"}
	seedTeamProject(t, "p1", alice.ID)
	seedTeamProject(t, "pa", alice.ID)
	helper := seedTeamAgent(t, alice.ID, "helper", "pa")
	tk := seedTicket(t, "p1", "With room")
	plain := seedTicket(t, "p1", "Without room")
	_, it := openRoom(t, alice, tk.ID)
	if code := addRoomMembers(t, alice, tk.ID, helper.ID); code != http.StatusOK {
		t.Fatalf("add: %d", code)
	}
	w, c := teamReq(t, alice, http.MethodGet, "/", nil, map[string]string{"id": "p1"})
	apiProjectTickets(c)
	if w.Code != http.StatusOK {
		t.Fatalf("board: %d %s", w.Code, w.Body)
	}
	var board struct {
		Tickets []TicketCard `json:"tickets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &board); err != nil {
		t.Fatal(err)
	}
	var withRoom, without *TicketCard
	for i := range board.Tickets {
		switch board.Tickets[i].ID {
		case tk.ID:
			withRoom = &board.Tickets[i]
		case plain.ID:
			without = &board.Tickets[i]
		}
	}
	if withRoom == nil || without == nil {
		t.Fatalf("cards: %+v", board.Tickets)
	}
	if without.Room != nil {
		t.Fatalf("ticket without a room has one: %+v", without.Room)
	}
	r := withRoom.Room
	if r == nil || r.SessionID != it.SessionID || len(r.Members) != 2 {
		t.Fatalf("room: %+v", r)
	}
	if r.Members[0].Kind != session.RoomMemberProjectDefault || r.Members[1].AgentID != helper.ID || r.Members[1].OwnerID != alice.ID || r.Members[1].Handle != "helper" {
		t.Fatalf("members: %+v", r.Members)
	}
	for _, row := range withRoom.SessionRows {
		if row.ID == it.SessionID {
			t.Fatal("the room is listed among the card's chats")
		}
	}
}
