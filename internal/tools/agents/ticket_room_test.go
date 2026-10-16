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
	"github.com/yogasw/wick/pkg/tool"
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
	tkNow, _ := ticket.Load(globalLayout, "p1", tk.ID)
	room, _ := findTicketRoom("p1", tkNow)
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

// openRoomIn is openRoom with the project named (?project=).
func openRoomIn(t *testing.T, u *entity.User, projectID, ticketID string) (int, TicketRoomItem) {
	t.Helper()
	w, c := teamReq(t, u, http.MethodPost, "/?project="+projectID, nil, map[string]string{"ticketID": ticketID})
	apiTicketRoomCreate(c)
	var it TicketRoomItem
	_ = json.Unmarshal(w.Body.Bytes(), &it)
	return w.Code, it
}

// One ticket id in two projects: the room is opened in the project the
// caller names, never in whichever the scan meets first, and each project's
// ticket gets its own room. Without the project the id is refused.
func TestTicketRoomSameTicketIDInTwoProjects(t *testing.T) {
	withTeamWorld(t)
	alice := &entity.User{ID: "alice"}
	seedTeamProject(t, "p1", alice.ID)
	seedTeamProject(t, "p2", alice.ID)
	const id = "0123456789abcdef0123456789abcdef"
	for _, p := range []string{"p1", "p2"} {
		if _, err := ticket.Create(globalLayout, ticket.CreateOptions{ProjectID: p, ID: id, Title: "Synced " + p}); err != nil {
			t.Fatal(err)
		}
	}
	if code, _ := openRoom(t, alice, id); code != http.StatusConflict {
		t.Fatalf("ambiguous id without a project: %d, want 409", code)
	}
	w, c := teamReq(t, alice, http.MethodGet, "/", nil, map[string]string{"ticketID": id})
	apiTicketRoomGet(c)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "ambiguous ticket id") {
		t.Fatalf("ambiguous GET: %d %s", w.Code, w.Body)
	}
	code, r2 := openRoomIn(t, alice, "p2", id)
	if code != http.StatusCreated || r2.ProjectID != "p2" {
		t.Fatalf("p2: %d %+v", code, r2)
	}
	w, c = teamReq(t, alice, http.MethodGet, "/?project=p1", nil, map[string]string{"ticketID": id})
	apiTicketRoomGet(c)
	if !strings.Contains(w.Body.String(), `"room":null`) {
		t.Fatalf("p1 should have no room yet: %s", w.Body)
	}
	code, r1 := openRoomIn(t, alice, "p1", id)
	if code != http.StatusCreated || r1.ProjectID != "p1" || r1.SessionID == r2.SessionID {
		t.Fatalf("p1: %d %+v (p2 room %s)", code, r1, r2.SessionID)
	}
	if code, again := openRoomIn(t, alice, "p2", id); code != http.StatusOK || again.SessionID != r2.SessionID {
		t.Fatalf("p2 again: %d %q, want 200 %q", code, again.SessionID, r2.SessionID)
	}
	// A project the caller cannot open is not found, whatever it holds.
	if code, _ := openRoomIn(t, &entity.User{ID: "mallory"}, "p1", id); code != http.StatusNotFound {
		t.Fatalf("outsider: %d", code)
	}
}

// Outsiders get 404 from every room route.
func TestTicketRoomRoutesNeedTheProject(t *testing.T) {
	withTeamWorld(t)
	alice, mallory := &entity.User{ID: "alice"}, &entity.User{ID: "mallory"}
	seedTeamProject(t, "p1", alice.ID)
	tk := seedTicket(t, "p1", "Private")
	openRoom(t, alice, tk.ID)
	vals := map[string]string{"ticketID": tk.ID, "agentID": "x"}
	for name, h := range map[string]func(*tool.Ctx){
		"get": apiTicketRoomGet, "patch": apiTicketRoomUpdate, "add": apiTicketRoomAddMembers, "remove": apiTicketRoomRemoveMember,
	} {
		w, c := teamReq(t, mallory, http.MethodPost, "/", map[string]any{"share_full": true, "agent_ids": []string{"x"}}, vals)
		h(c)
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s as outsider: %d, want 404", name, w.Code)
		}
	}
}

// A room is not a chat: the generic session routes refuse what does not
// apply to it, and deleting it waits for its ticket.
func TestTicketRoomGenericSessionRoutes(t *testing.T) {
	withTeamWorld(t)
	alice := &entity.User{ID: "alice"}
	seedTeamProject(t, "p1", alice.ID)
	tk := seedTicket(t, "p1", "Room")
	_, it := openRoom(t, alice, tk.ID)
	called := false
	next := func(c *tool.Ctx) { called = true; c.JSON(http.StatusOK, map[string]string{"status": "ok"}) }
	try := func(method, path string) int {
		called = false
		w, c := teamReq(t, alice, method, path, map[string]any{}, map[string]string{"id": it.SessionID})
		sessionAccessMW(next)(c)
		return w.Code
	}
	if code := try(http.MethodGet, "/api/sessions/"+it.SessionID+"/conversation"); code != http.StatusOK || !called {
		t.Fatalf("read: %d", code)
	}
	if code := try(http.MethodPost, "/sessions/"+it.SessionID+"/send"); code != http.StatusOK || !called {
		t.Fatalf("send: %d", code)
	}
	// The controls a member needs: stop a stuck run, follow the room.
	for _, p := range []string{"/kill", "/runs/run-1/cancel", "/dequeue", "/subscribe", "/unsubscribe"} {
		if code := try(http.MethodPost, "/api/sessions/"+it.SessionID+p); code != http.StatusOK || !called {
			t.Fatalf("POST %s: %d, want through", p, code)
		}
	}
	for _, p := range []string{"/project", "/provider", "/schedules", "/workspace/files", "/runs/run-1/retry", "/runs//cancel", "/kill/x"} {
		if code := try(http.MethodPost, "/sessions/"+it.SessionID+p); code != http.StatusBadRequest || called {
			t.Fatalf("POST %s: %d, want 400", p, code)
		}
	}
	if code := try(http.MethodDelete, "/sessions/"+it.SessionID); code != http.StatusBadRequest || called {
		t.Fatalf("delete while the ticket exists: %d, want 400", code)
	}
	// Moving the room to another ticket, or off this one, is refused too.
	other := seedTicket(t, "p1", "Other")
	w, c := teamReq(t, alice, http.MethodPut, "/", nil, map[string]string{"ticketID": other.ID, "sid": it.SessionID})
	apiTicketAttachSession(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("attach the room elsewhere: %d", w.Code)
	}
	w, c = teamReq(t, alice, http.MethodDelete, "/", nil, map[string]string{"ticketID": tk.ID, "sid": it.SessionID})
	apiTicketDetachSession(c)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("detach the room: %d", w.Code)
	}
	// Deleting the ticket takes its room along, with the backing sessions
	// and the sub-agents under them.
	_, c = teamReq(t, alice, http.MethodPost, "/", nil, nil)
	backing, err := roomDefaultBacking(c, it.SessionID, "p1")
	if err != nil {
		t.Fatal(err)
	}
	parent := backing
	var subs []string
	for _, id := range []string{"sub-a", "sub-b", "sub-c"} {
		sub, err := globalMgr.CreateSession(context.Background(), session.CreateOptions{ID: id, ProjectID: "p1", UserID: alice.ID, ParentSessionID: parent})
		if err != nil {
			t.Fatal(err)
		}
		subs = append(subs, sub.ID)
		parent = sub.ID
	}
	// A session outside the room, in the same project, is not its.
	control, err := globalMgr.CreateSession(context.Background(), session.CreateOptions{ID: "control", ProjectID: "p1", UserID: alice.ID})
	if err != nil {
		t.Fatal(err)
	}
	w, c = teamReq(t, alice, http.MethodDelete, "/", nil, map[string]string{"ticketID": tk.ID})
	apiTicketDelete(c)
	if w.Code != http.StatusOK {
		t.Fatalf("delete ticket: %d %s", w.Code, w.Body)
	}
	for _, sid := range append([]string{it.SessionID, backing}, subs...) {
		if _, ok := globalMgr.Registry().Session(sid); ok {
			t.Fatalf("session %s outlived its ticket", sid)
		}
	}
	if _, ok := globalMgr.Registry().Session(control.ID); !ok {
		t.Fatal("a session outside the room went with the ticket")
	}
}

// Stopping a room stops the caller's own members — their backing
// sessions, where the runs are — and never another member's.
func TestTicketRoomStopReachesOnlyTheCallersBacking(t *testing.T) {
	withTeamWorld(t)
	alice, bob := &entity.User{ID: "alice", Role: entity.RoleAdmin}, &entity.User{ID: "bob", Role: entity.RoleAdmin}
	seedTeamProject(t, "p1", "")
	tk := seedTicket(t, "p1", "Room")
	_, it := openRoom(t, alice, tk.ID)
	backingOf := func(u *entity.User) string {
		_, c := teamReq(t, u, http.MethodPost, "/", nil, nil)
		id, err := roomDefaultBacking(c, it.SessionID, "p1")
		if err != nil {
			t.Fatal(err)
		}
		// Busy: a turn waiting to run.
		if err := saveGroupMeta(id, func(m *session.Meta) { m.Status = session.StatusQueued }); err != nil {
			t.Fatal(err)
		}
		return id
	}
	mine, theirs := backingOf(alice), backingOf(bob)

	var stopped []string
	prev := stopSessionRun
	stopSessionRun = func(_ *tool.Ctx, s session.Session) (int, any) {
		stopped = append(stopped, s.ID)
		return http.StatusOK, map[string]string{"status": "killed"}
	}
	t.Cleanup(func() { stopSessionRun = prev })
	w, c := teamReq(t, alice, http.MethodPost, "/api/sessions/"+it.SessionID+"/kill", nil, map[string]string{"id": it.SessionID})
	killAgent(c)
	if w.Code != http.StatusOK {
		t.Fatalf("kill room: %d %s", w.Code, w.Body)
	}
	if len(stopped) != 1 || stopped[0] != mine {
		t.Fatalf("stopped %v, want only alice's backing %s", stopped, mine)
	}

	w, c = teamReq(t, alice, http.MethodPost, "/api/sessions/"+it.SessionID+"/dequeue", nil, map[string]string{"id": it.SessionID})
	dequeueAgent(c)
	if w.Code != http.StatusOK {
		t.Fatalf("dequeue room: %d %s", w.Code, w.Body)
	}
	if s, _ := globalMgr.Registry().Session(mine); s.Meta.Status != session.StatusIdle {
		t.Fatalf("alice's backing still %q", s.Meta.Status)
	}
	if s, _ := globalMgr.Registry().Session(theirs); s.Meta.Status != session.StatusQueued {
		t.Fatalf("bob's backing touched: %q", s.Meta.Status)
	}
	// A connector run of bob's backing is not alice's to cancel from the room.
	_, c = teamReq(t, alice, http.MethodPost, "/", nil, nil)
	if room, _ := globalMgr.Registry().Session(it.SessionID); !runInOwnRoomBacking(c, room, mine) || runInOwnRoomBacking(c, room, theirs) {
		t.Fatal("run ownership in the room")
	}
}

// Backing sessions are their person's alone: not in the project's lists,
// not on the board's Untracked rail, not openable by another member — and
// the project default's reaches nothing of the project but its ticket.
func TestTicketRoomBackingSessionsStayPrivateAndScoped(t *testing.T) {
	withTeamWorld(t)
	alice, bob := &entity.User{ID: "alice"}, &entity.User{ID: "bob"}
	seedTeamProject(t, "p1", "")
	tk := seedTicket(t, "p1", "Room")
	other := seedTicket(t, "p1", "Other")
	// The project is ownerless (admins only), so open it as admins.
	alice.Role, bob.Role = entity.RoleAdmin, entity.RoleAdmin
	_, it := openRoom(t, alice, tk.ID)
	_, c := teamReq(t, alice, http.MethodPost, "/", nil, nil)
	backing, err := roomDefaultBacking(c, it.SessionID, "p1")
	if err != nil {
		t.Fatal(err)
	}
	bs, _ := globalMgr.Registry().Session(backing)
	if bs.Meta.ProjectID != "p1" || bs.Meta.AgentID != "" {
		t.Fatalf("backing meta: %+v", bs.Meta)
	}
	// Bob may open the room, never alice's backing.
	_, cb := teamReq(t, bob, http.MethodGet, "/", nil, nil)
	if room, _ := globalMgr.Registry().Session(it.SessionID); !ownsSession(cb, room) {
		t.Fatal("bob cannot open the room")
	}
	if ownsSession(cb, bs) {
		t.Fatal("bob opens alice's backing session")
	}
	_, ca := teamReq(t, alice, http.MethodGet, "/", nil, nil)
	if !ownsSession(ca, bs) {
		t.Fatal("alice cannot open her own backing session")
	}
	// Not untracked on the board.
	w, cw := teamReq(t, alice, http.MethodGet, "/?untracked=1", nil, map[string]string{"id": "p1"})
	apiProjectTickets(cw)
	if strings.Contains(w.Body.String(), backing) {
		t.Fatalf("backing session on the board: %s", w.Body)
	}
	// Scoped to its ticket: not the project, not another chat.
	ctx := context.Background()
	if err := team.CheckProjectTarget(ctx, globalLayout, alice.ID, backing, "p1"); err == nil {
		t.Fatal("the project default's backing reaches its whole project")
	}
	if err := team.CheckSessionTarget(ctx, globalLayout, alice.ID, backing, it.SessionID); err == nil {
		t.Fatal("the project default's backing reaches another session")
	}
	if p, tid, ok := team.RoomTicket(ctx, globalLayout, backing); !ok || p != "p1" || tid != tk.ID || tid == other.ID {
		t.Fatalf("room ticket: %q %q %v", p, tid, ok)
	}
}

// Naming someone else's agent posts a system line and runs nothing: no
// backing session is even made.
func TestTicketRoomForeignMentionRunsNothing(t *testing.T) {
	withTeamWorld(t)
	alice := &entity.User{ID: "alice"}
	seedTeamProject(t, "p1", alice.ID)
	tk := seedTicket(t, "p1", "Room")
	_, it := openRoom(t, alice, tk.ID)
	room, _ := globalMgr.Registry().Session(it.SessionID)
	w, c := teamReq(t, alice, http.MethodPost, "/", nil, nil)
	sendRoomMessage(c, room, "@hana/captain please check the logs")
	if w.Code != http.StatusOK {
		t.Fatalf("send: %d %s", w.Code, w.Body)
	}
	turns, _ := loadConversation(globalLayout, it.SessionID)
	refused := false
	for _, tr := range turns {
		if tr.Role == "system" && tr.Kind == store.KindMentionRefused && strings.Contains(tr.Text, "only takes instructions from @hana") {
			refused = true
		}
		if tr.Role == "assistant" {
			t.Fatalf("an agent answered: %+v", tr)
		}
	}
	if !refused {
		t.Fatalf("no system line: %+v", turns)
	}
	for _, s := range globalMgr.Registry().Sessions() {
		if s.Meta.GroupSessionID == it.SessionID {
			t.Fatalf("a backing session was made: %s", s.ID)
		}
	}
}

// A refused foreign mention names the agent the way the room shows it,
// and points the sender at their own Captain by its real handle.
func TestTicketRoomForeignLabelAndCaptainHint(t *testing.T) {
	f := foreignMention{Owner: "hana", Handle: "scout"}
	if got := foreignLabel(f, ""); got != "@scout" {
		t.Fatalf("unresolved label = %q", got)
	}
	if got := foreignLabel(f, "Scout"); got != `"Scout"` {
		t.Fatalf("member label = %q", got)
	}
	if got := ownCaptainHint([]teamlink.Peer{{Handle: "ops"}, {Handle: "skipper", IsCaptain: true}}); got != "mention your own @skipper" {
		t.Fatalf("hint = %q", got)
	}
	if got := ownCaptainHint(nil); got != "mention one of your own agents" {
		t.Fatalf("hint without a Captain = %q", got)
	}
}
