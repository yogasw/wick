package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// Ticket rooms (PLAN-team-tickets §4, §5, §8.4).
//
// A room is a group chat that belongs to one ticket: a session in the
// ticket's project with Meta.TicketRoom, attached to the ticket like any
// chat, that never spawns a provider. Members answer from backing
// sessions of their own (GroupSessionID = the room), exactly as group
// members do (team_group.go) — with one difference that drives every rule
// here: a room is shared by several people, and an agent works only for
// the person who brought it in, with that person's access.
//
//   - Only a person adds agents: by @mentioning one of their own Team
//     agents, or in a batch (POST …/room/members). Nothing joins by itself.
//   - An agent answers only the person it works for. Naming someone else's
//     agent ("@hana/captain", or a handle that is not one of yours) posts
//     a system line and runs nothing.
//   - The home member (the project's own Team agent when it is the
//     creator's, else the project default) is always there; a room may
//     have just that one member.
//   - What a member reads is built per person: that person's messages, its
//     own replies, the ticket and system lines; everyone else's turns only
//     when that person turned ShareFull on.
//   - Its backing session reaches the room's ticket and nothing else of
//     the project (team.RoomTicket).

const (
	// roomHomePeerID is the project default's peer id in a room's routing.
	roomHomePeerID = "project-default"
	// roomHomeHandle is how the project default is named in a room.
	roomHomeHandle = "project"
	// roomTicketBodyMax caps the ticket description a member's turn carries.
	roomTicketBodyMax = 1500
	// errRoomSession refuses what a room (or a member's backing session)
	// does not support through the generic session and ticket routes.
	errRoomSession = "not supported in a ticket room"
)

// errLeftRoom is a queued turn of a member that left the room meanwhile.
var errLeftRoom = errors.New("is no longer in this room")

/* ── DTOs ────────────────────────────────────────────────────────────── */

// TicketRoomMemberItem is one member as the room's strip shows it.
type TicketRoomMemberItem struct {
	Kind    string `json:"kind"`
	AgentID string `json:"agent_id,omitempty"`
	Handle  string `json:"handle"`
	Name    string `json:"name"`
	// OwnerID / OwnerHandle / OwnerName: whom the member works for; empty
	// for the project default, which works for whoever sends.
	OwnerID     string       `json:"owner_id,omitempty"`
	OwnerHandle string       `json:"owner_handle,omitempty"`
	OwnerName   string       `json:"owner_name,omitempty"`
	Home        bool         `json:"home,omitempty"`
	IsCaptain   bool         `json:"is_captain,omitempty"`
	Avatar      *team.Avatar `json:"avatar,omitempty"`
	// Mine: the caller can address it (their own agent, or the project
	// default); anyone else's is shown greyed.
	Mine bool `json:"mine"`
}

// TicketRoomItem is a room as the caller sees it.
type TicketRoomItem struct {
	SessionID string                 `json:"session_id"`
	TicketID  string                 `json:"ticket_id"`
	ProjectID string                 `json:"project_id"`
	Members   []TicketRoomMemberItem `json:"members"`
	// ShareFull is the caller's own toggle.
	ShareFull bool `json:"share_full"`
	// Responder is who a message of the caller's with no @ goes to ("" =
	// nobody the caller may address).
	Responder string `json:"responder"`
}

/* ── pure rules ──────────────────────────────────────────────────────── */

// roomAddMembers adds ids, which must all be owner's own agents (own), to
// tr as members working for owner, and returns the ones not there yet.
// All or nothing: one id that is not owner's refuses the batch.
func roomAddMembers(tr *session.TicketRoom, ids []string, own []teamlink.Peer, owner string) ([]teamlink.Peer, error) {
	byID := make(map[string]teamlink.Peer, len(own))
	for _, p := range own {
		byID[p.ID] = p
	}
	seen := map[string]bool{}
	var add []teamlink.Peer
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		p, ok := byID[id]
		if !ok || p.OwnerID != owner {
			return nil, fmt.Errorf("%q is not one of your agents", id)
		}
		if _, in := tr.Member(id, owner); in {
			continue
		}
		add = append(add, p)
	}
	for _, p := range add {
		tr.Members = append(tr.Members, session.RoomMember{Kind: session.RoomMemberAgent, AgentID: p.ID, OwnerID: owner})
	}
	return add, nil
}

// roomRemoveMember drops owner's agentID from tr. The home member stays.
func roomRemoveMember(tr *session.TicketRoom, agentID, owner string) error {
	for i, m := range tr.Members {
		if m.Kind == session.RoomMemberAgent && m.AgentID == agentID && m.OwnerID == owner {
			if m.Home {
				return errors.New("the home member cannot be removed")
			}
			tr.Members = append(tr.Members[:i:i], tr.Members[i+1:]...)
			return nil
		}
	}
	return errors.New("not one of your agents in this room")
}

// foreignMention is "@owner/handle" at a line start: another person's agent.
type foreignMention struct{ Owner, Handle string }

// foreignLabel names another person's agent in a system line: its name
// when it is already in the room (roomForeignAgent), else its @handle —
// a handle names no agent until its owner is known.
func foreignLabel(f foreignMention, name string) string {
	if name = strings.TrimSpace(name); name != "" {
		return name
	}
	return "@" + f.Handle
}

// roomForeignAgent is the display name of the member of tr that f names
// — another person's agent already in the room — or "". Only the room's
// own members are looked up: resolving any person's Team from a handle
// would cost a user scan per mention.
func roomForeignAgent(ctx context.Context, tr *session.TicketRoom, f foreignMention, rc *roomCards) string {
	if tr == nil || globalTeam == nil {
		return ""
	}
	for _, m := range tr.Members {
		if m.Kind != session.RoomMemberAgent || m.OwnerID == "" || rc.handle(ctx, m.OwnerID) != f.Owner {
			continue
		}
		if a := rc.agent(ctx, m.AgentID); a != nil && teamlink.NormalizeHandle(a.Handle) == f.Handle {
			return globalTeam.MemberOf(*a).Name
		}
	}
	return ""
}

// ownCaptainHint is how a refused mention points the sender back to
// their own agents: their Captain by its handle, when they have one.
func ownCaptainHint(own []teamlink.Peer) string {
	for _, p := range own {
		if p.IsCaptain && !p.Disabled {
			return "mention your own @" + p.Handle
		}
	}
	return "mention one of your own agents"
}

// foreignMentions lists, once each, the "@owner/handle" mentions at line
// starts outside fenced code — agents of someone else, which never run on
// the sender's message.
func foreignMentions(text string) []foreignMention {
	var out []foreignMention
	seen := map[string]bool{}
	inFence := false
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "```") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(t, "@") {
			continue
		}
		word := t[1:]
		if cut := strings.IndexAny(word, " \t,:"); cut >= 0 {
			word = word[:cut]
		}
		owner, handle, ok := strings.Cut(word, "/")
		if !ok || owner == "" || handle == "" || seen[word] {
			continue
		}
		seen[word] = true
		out = append(out, foreignMention{Owner: strings.ToLower(owner), Handle: teamlink.NormalizeHandle(handle)})
	}
	return out
}

// roomSenderPeers is who in tr answers sender: sender's own agent members
// (resolved through own; a deleted agent drops out) and the home member
// when sender may address it. home is the project default's peer.
func roomSenderPeers(tr *session.TicketRoom, sender string, own map[string]teamlink.Peer, home teamlink.Peer) []teamlink.Peer {
	var out []teamlink.Peer
	for _, m := range tr.Members {
		switch {
		case m.Kind == session.RoomMemberProjectDefault:
			out = append(out, home)
		case m.Kind == session.RoomMemberAgent && m.OwnerID == sender:
			if p, ok := own[m.AgentID]; ok {
				out = append(out, p)
			}
		}
	}
	return out
}

// roomDefaultResponder answers a message of sender's with no @: sender's
// Captain when it is a member, else the home member when sender may
// address it. ok false = nobody (the home agent is someone else's).
func roomDefaultResponder(tr *session.TicketRoom, peers []teamlink.Peer) (teamlink.Peer, bool) {
	for _, p := range peers {
		if p.IsCaptain && !p.Disabled && p.ID != roomHomePeerID {
			return p, true
		}
	}
	home, has := tr.Home()
	if !has {
		return teamlink.Peer{}, false
	}
	for _, p := range peers {
		if p.Disabled {
			continue
		}
		if (home.Kind == session.RoomMemberProjectDefault && p.ID == roomHomePeerID) || (home.Kind == session.RoomMemberAgent && p.ID == home.AgentID) {
			return p, true
		}
	}
	return teamlink.Peer{}, false
}

// roomPlan is who a person's room message reaches.
type roomPlan struct {
	// Join is the sender's agents named that are not members yet; they
	// join before answering.
	Join []string
	// Peers is who answers the sender in the room (after Join); Targets
	// those that answer this message.
	Peers, Targets []teamlink.Peer
	// Foreign: someone else's agents named ("@hana/captain"); Unknown:
	// handles that are none of the sender's agents; Refused: the sender's
	// disabled agents named. None of them runs.
	Foreign   []foreignMention
	Unknown   []string
	Refused   []string
	NoDefault bool // no @ and nobody the sender may address
}

// planRoomMessage routes text from sender in a room shaped tr (a copy;
// not changed). Only the sender's own agents (own) can be named, joined or
// run; a message naming nobody goes to the default responder, while one
// that names only agents that cannot run goes to nobody.
func planRoomMessage(text string, tr session.TicketRoom, sender string, own []teamlink.Peer, home teamlink.Peer) (roomPlan, error) {
	var plan roomPlan
	byID := make(map[string]teamlink.Peer, len(own))
	for _, p := range own {
		byID[p.ID] = p
	}
	plan.Foreign = foreignMentions(text)
	hit, unknown := teamlink.LeadMentions(text, own, false)
	plan.Unknown = unknown
	tr.Members = append([]session.RoomMember(nil), tr.Members...)
	if len(hit) > 0 {
		// A disabled agent named does not join: it could not answer.
		ids := make([]string, 0, len(hit))
		for _, p := range hit {
			if !p.Disabled {
				ids = append(ids, p.ID)
			}
		}
		added, err := roomAddMembers(&tr, ids, own, sender)
		if err != nil {
			return roomPlan{}, err
		}
		for _, p := range added {
			plan.Join = append(plan.Join, p.ID)
		}
	}
	plan.Peers = roomSenderPeers(&tr, sender, byID, home)
	for _, p := range hit {
		if p.Disabled {
			plan.Refused = append(plan.Refused, p.Handle)
			continue
		}
		plan.Targets = append(plan.Targets, p)
	}
	if len(hit) == 0 && len(plan.Foreign) == 0 && len(unknown) == 0 {
		if p, ok := roomDefaultResponder(&tr, plan.Peers); ok {
			plan.Targets = []teamlink.Peer{p}
		} else {
			plan.NoDefault = true
		}
	}
	return plan, nil
}

// roomPromptIn is what a member's turn is built from.
type roomPromptIn struct {
	Ticket    ticket.Ticket
	Self      teamlink.Peer
	From      string // handle that handed the turn; "" = a person
	Sender    string // the person Self works for
	ShareFull bool
	Peers     []teamlink.Peer // Sender's members, for @ hand-offs
	History   []store.ConversationTurn
	// OwnerOf is whose a turn is: the person who wrote it, or the person
	// the agent that wrote it works for; "" for a system line.
	OwnerOf func(store.ConversationTurn) string
	// HandleOf is a person's handle (wick username).
	HandleOf func(uid string) string
}

// roomPrompt is what a member's backing session receives. Only the part
// of the thread that belongs to Sender is in it — Sender's messages, the
// replies of Sender's agents, system lines — unless Sender shares the full
// thread; then other people and their agents are named "Name (@owner)".
func roomPrompt(in roomPromptIn) string {
	var b strings.Builder
	handles := make([]string, 0, len(in.Peers))
	for _, p := range in.Peers {
		if p.ID != in.Self.ID {
			handles = append(handles, "@"+p.Handle)
		}
	}
	also := strings.Join(handles, ", ")
	if also == "" {
		also = "nobody else of yours"
	}
	fmt.Fprintf(&b, "[Ticket room of %s %q (status: %s) — you are @%s, working for @%s; also yours here: %s]\n",
		in.Ticket.ID, in.Ticket.Title, in.Ticket.Status, in.Self.Handle, in.HandleOf(in.Sender), also)
	b.WriteString("Your reply is posted to the room as yours. You may read and update this ticket and its notes; other tickets of the project are closed to you. Start a line with @handle and a message to hand one of the agents above a turn; only do so when you need them.\n")
	if body := strings.TrimSpace(in.Ticket.Body); body != "" {
		if r := []rune(body); len(r) > roomTicketBodyMax {
			body = string(r[:roomTicketBodyMax-1]) + "…"
		}
		fmt.Fprintf(&b, "\nTicket description:\n%s\n", body)
	}
	msgs := make([]store.ConversationTurn, 0, groupContextTurns)
	for _, t := range in.History {
		if t.Role != "user" && t.Role != "assistant" && t.Role != "system" {
			continue
		}
		if t.Role != "system" && !in.ShareFull && in.OwnerOf(t) != in.Sender {
			continue
		}
		msgs = append(msgs, t)
	}
	if len(msgs) > groupContextTurns {
		msgs = msgs[len(msgs)-groupContextTurns:]
	}
	b.WriteString("\nRecent messages:\n")
	for _, t := range msgs {
		owner := in.OwnerOf(t)
		var who string
		switch {
		case t.Role == "system":
			who = "system"
		case t.Speaker != nil && owner == in.Sender:
			who = fmt.Sprintf("@%s (agent %s)", t.Speaker.Handle, t.Speaker.AgentID)
		case t.Speaker != nil:
			who = fmt.Sprintf("%s (@%s)", t.Speaker.Handle, in.HandleOf(owner))
		case owner == in.Sender:
			who = "@" + in.HandleOf(owner)
		default:
			name := in.HandleOf(owner)
			if t.Sender != nil && t.Sender.Name != "" {
				name = t.Sender.Name
			}
			who = fmt.Sprintf("%s (@%s)", name, in.HandleOf(owner))
		}
		fmt.Fprintf(&b, "--- %s:\n%s\n", who, strings.TrimSpace(t.Text))
	}
	if in.From != "" {
		fmt.Fprintf(&b, "\n@%s handed you this turn. Answer the last message.", in.From)
	} else {
		b.WriteString("\nAnswer the last message.")
	}
	return b.String()
}

/* ── lookups ─────────────────────────────────────────────────────────── */

// ownTeamPeers is uid's own Team agents (never the ones shared with them:
// a shared agent stays chat-only and out of rooms).
func ownTeamPeers(ctx context.Context, uid string) (map[string]teamlink.Peer, []teamlink.Peer) {
	if globalTeam == nil || uid == "" {
		return map[string]teamlink.Peer{}, nil
	}
	own, err := teamDirectory{svc: globalTeam}.Peers(ctx, uid)
	if err != nil {
		return map[string]teamlink.Peer{}, nil
	}
	byID := make(map[string]teamlink.Peer, len(own))
	for _, p := range own {
		byID[p.ID] = p
	}
	return byID, own
}

// findTicketRoom is the room of projectID's ticket tk, if one exists and
// is still on the ticket: a room the ticket no longer lists (moved off it,
// or a ticket re-created under the same id) is not its room.
func findTicketRoom(projectID string, tk ticket.Ticket) (session.Session, bool) {
	for _, s := range globalMgr.Registry().Sessions() {
		if tr := s.Meta.TicketRoom; tr != nil && tr.TicketID == tk.ID && s.Meta.ProjectID == projectID && roomOnTicket(s.ID, tk) {
			return s, true
		}
	}
	return session.Session{}, false
}

func roomOnTicket(roomID string, tk ticket.Ticket) bool {
	for _, sid := range tk.Sessions {
		if sid == roomID {
			return true
		}
	}
	return false
}

// endTicketRooms deletes the rooms of projectID's ticketID, their
// members' backing sessions and every sub-agent session under those — a
// deleted ticket's room has nothing left to work on, and a sub-agent left
// behind would be an orphan on disk.
func endTicketRooms(ctx context.Context, projectID, ticketID string) {
	all := globalMgr.Registry().Sessions()
	for _, s := range all {
		tr := s.Meta.TicketRoom
		if tr == nil || tr.TicketID != ticketID || s.Meta.ProjectID != projectID {
			continue
		}
		// Breadth-first from the backing sessions down the parent links,
		// then deleted deepest first so no child outlives its parent.
		var doomed []string
		seen := map[string]bool{}
		for _, b := range all {
			if b.Meta.GroupSessionID == s.ID && !seen[b.ID] {
				seen[b.ID] = true
				doomed = append(doomed, b.ID)
			}
		}
		for i := 0; i < len(doomed); i++ {
			for _, c := range all {
				if c.Meta.ParentSessionID == doomed[i] && !seen[c.ID] {
					seen[c.ID] = true
					doomed = append(doomed, c.ID)
				}
			}
		}
		for i := len(doomed) - 1; i >= 0; i-- {
			stopRoomSession(ctx, doomed[i])
			_ = globalMgr.DeleteSession(ctx, doomed[i])
		}
		if err := globalMgr.DeleteSession(ctx, s.ID); err != nil {
			log.Ctx(ctx).Warn().Err(err).Str("session", s.ID).Msg("ticket room: delete with its ticket failed")
		}
	}
}

// stopRoomSession ends whatever sid still has queued or running before
// its files go: a backing session or sub-agent can be mid-turn when its
// ticket is deleted. Best effort — the delete goes ahead either way.
func stopRoomSession(ctx context.Context, sid string) {
	if globalPool == nil {
		return
	}
	globalPool.DequeueSession(sid)
	if err := globalPool.KillBy(sid, "", "wick", "its ticket was deleted"); err != nil {
		log.Ctx(ctx).Warn().Err(err).Str("session", sid).Msg("ticket room: stop before delete failed")
	}
}

// roomHomePeer is the project default as a peer.
func roomHomePeer(projectID string) teamlink.Peer {
	name := "Project"
	if p, ok := globalMgr.Registry().Project(projectID); ok && p.Meta.Name != "" {
		name = p.Meta.Name
	}
	return teamlink.Peer{ID: roomHomePeerID, Handle: roomHomeHandle, Name: name}
}

// personHandle is a person's handle in a room: the wick username (the
// e-mail's local part), else the id.
func personHandle(ctx context.Context, uid string) string {
	if globalAuth != nil && uid != "" {
		if u, err := globalAuth.GetUserByID(ctx, uid); err == nil && u != nil {
			if at := strings.IndexByte(u.Email, '@'); at > 0 {
				return strings.ToLower(u.Email[:at])
			}
		}
	}
	return uid
}

// personName is a person's display name, else their handle.
func personName(ctx context.Context, uid string) string {
	if globalAuth != nil && uid != "" {
		if u, err := globalAuth.GetUserByID(ctx, uid); err == nil && u != nil {
			return displayNameOf(*u)
		}
	}
	return uid
}

// roomTicketFor resolves {ticketID} for the caller: its project (the
// caller must be able to open it) and the ticket.
func roomTicketFor(c *tool.Ctx) (string, ticket.Ticket, bool) {
	if notReady(c) {
		return "", ticket.Ticket{}, false
	}
	id := c.PathValue("ticketID")
	projectID, ok := ticketProjectOrAnswer(c, id)
	if !ok {
		return "", ticket.Ticket{}, false
	}
	tk, err := ticket.Load(globalLayout, projectID, id)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "ticket not found"})
		return "", ticket.Ticket{}, false
	}
	return projectID, tk, true
}

func roomItem(ctx context.Context, sess session.Session, caller string) TicketRoomItem {
	tr := sess.Meta.TicketRoom
	own, _ := ownTeamPeers(ctx, caller)
	home := roomHomePeer(sess.Meta.ProjectID)
	it := TicketRoomItem{SessionID: sess.ID, TicketID: tr.TicketID, ProjectID: sess.Meta.ProjectID, ShareFull: tr.ShareFull[caller], Members: []TicketRoomMemberItem{}}
	for _, m := range tr.Members {
		if m.Kind == session.RoomMemberProjectDefault {
			it.Members = append(it.Members, TicketRoomMemberItem{Kind: m.Kind, Handle: home.Handle, Name: home.Name, Home: m.Home, Mine: true})
			continue
		}
		mi := TicketRoomMemberItem{Kind: m.Kind, AgentID: m.AgentID, OwnerID: m.OwnerID, Home: m.Home, Mine: m.OwnerID == caller}
		if globalTeam != nil {
			if a, err := globalTeam.Get(ctx, m.AgentID); err == nil {
				mi.Handle, mi.Name, mi.IsCaptain = a.Handle, globalTeam.MemberOf(a).Name, a.IsCaptain
				av := team.WithImageSrc(team.DecodeAvatar(a.Avatar), agentsToolBase, a.ID)
				mi.Avatar = &av
			}
		}
		if mi.Handle == "" {
			continue // the agent was deleted
		}
		mi.OwnerHandle, mi.OwnerName = personHandle(ctx, m.OwnerID), personName(ctx, m.OwnerID)
		it.Members = append(it.Members, mi)
	}
	if p, ok := roomDefaultResponder(tr, roomSenderPeers(tr, caller, own, home)); ok {
		it.Responder = p.Handle
	}
	return it
}

// TicketCardRoom is a room as a board card shows it: who is in it.
type TicketCardRoom struct {
	SessionID string                `json:"session_id"`
	Members   []TicketCardRoomAgent `json:"members"`
}

// TicketCardRoomAgent is one member on a card.
type TicketCardRoomAgent struct {
	Kind        string       `json:"kind"`
	AgentID     string       `json:"agent_id,omitempty"`
	Handle      string       `json:"handle"`
	Name        string       `json:"name"`
	OwnerID     string       `json:"owner_id,omitempty"`
	OwnerHandle string       `json:"owner_handle,omitempty"`
	Avatar      *team.Avatar `json:"avatar,omitempty"`
}

// ticketRoomsOf maps each ticket of projectID that has a room to it, in
// one pass over the sessions (the board asks for all its cards at once).
func ticketRoomsOf(projectID string) map[string]session.Session {
	out := map[string]session.Session{}
	for _, s := range globalMgr.Registry().Sessions() {
		if s.Meta.TicketRoom != nil && s.Meta.ProjectID == projectID {
			out[s.Meta.TicketRoom.TicketID] = s
		}
	}
	return out
}

// roomCards builds board cards' rooms for one request, reading each agent
// and each owner's handle once however many cards they sit on.
type roomCards struct {
	agents  map[string]*entity.AgentPersona // nil = deleted
	handles map[string]string
}

func newRoomCards() *roomCards {
	return &roomCards{agents: map[string]*entity.AgentPersona{}, handles: map[string]string{}}
}

func (rc *roomCards) agent(ctx context.Context, id string) *entity.AgentPersona {
	if a, ok := rc.agents[id]; ok {
		return a
	}
	var out *entity.AgentPersona
	if globalTeam != nil {
		if a, err := globalTeam.Get(ctx, id); err == nil {
			out = &a
		}
	}
	rc.agents[id] = out
	return out
}

func (rc *roomCards) handle(ctx context.Context, uid string) string {
	if h, ok := rc.handles[uid]; ok {
		return h
	}
	h := personHandle(ctx, uid)
	rc.handles[uid] = h
	return h
}

// cardOf is sess's members for a board card, in room order. The same for
// every viewer: a card shows who is in the room, not who answers whom.
func (rc *roomCards) cardOf(ctx context.Context, sess session.Session) *TicketCardRoom {
	out := &TicketCardRoom{SessionID: sess.ID, Members: []TicketCardRoomAgent{}}
	for _, m := range sess.Meta.TicketRoom.Members {
		if m.Kind == session.RoomMemberProjectDefault {
			home := roomHomePeer(sess.Meta.ProjectID)
			out.Members = append(out.Members, TicketCardRoomAgent{Kind: m.Kind, Handle: home.Handle, Name: home.Name})
			continue
		}
		a := rc.agent(ctx, m.AgentID)
		if a == nil {
			continue // the agent was deleted
		}
		av := team.WithImageSrc(team.DecodeAvatar(a.Avatar), agentsToolBase, a.ID)
		out.Members = append(out.Members, TicketCardRoomAgent{
			Kind: m.Kind, AgentID: a.ID, Handle: a.Handle, Name: globalTeam.MemberOf(*a).Name,
			OwnerID: m.OwnerID, OwnerHandle: rc.handle(ctx, m.OwnerID), Avatar: &av,
		})
	}
	return out
}

/* ── HTTP ────────────────────────────────────────────────────────────── */

// roomCreateMu keeps two quick "open room" calls from making two rooms.
var roomCreateMu sync.Mutex

// apiTicketRoomGet handles GET /api/tickets/{ticketID}/room: the room as
// the caller sees it, or {"room": null} when the ticket has none yet.
func apiTicketRoomGet(c *tool.Ctx) {
	projectID, tk, ok := roomTicketFor(c)
	if !ok {
		return
	}
	sess, found := findTicketRoom(projectID, tk)
	if !found {
		c.JSON(http.StatusOK, map[string]any{"room": nil})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"room": roomItem(c.Context(), sess, actorID(c))})
}

// apiTicketRoomCreate handles POST /api/tickets/{ticketID}/room: create
// the ticket's room, or return the one it has. The room lives in the
// ticket's project, belongs to its creator, starts with the home member
// and is attached to the ticket.
func apiTicketRoomCreate(c *tool.Ctx) {
	projectID, tk, ok := roomTicketFor(c)
	if !ok {
		return
	}
	roomCreateMu.Lock()
	defer roomCreateMu.Unlock()
	if sess, found := findTicketRoom(projectID, tk); found {
		c.JSON(http.StatusOK, roomItem(c.Context(), sess, actorID(c)))
		return
	}
	creator := actorID(c)
	home := session.RoomMember{Kind: session.RoomMemberProjectDefault, Home: true}
	if session.ProjectAgent != nil && globalTeam != nil {
		// The project's own Team agent is the home member when it is the
		// creator's; anyone else's would not take the creator's messages.
		if aid := session.ProjectAgent(projectID); aid != "" {
			if a, err := globalTeam.Get(c.Context(), aid); err == nil && a.OwnerUserID == creator {
				home = session.RoomMember{Kind: session.RoomMemberAgent, AgentID: a.ID, OwnerID: creator, Home: true}
			}
		}
	}
	id := uuid.New().String()
	if _, err := globalMgr.CreateSession(c.Context(), session.CreateOptions{ID: id, ProjectID: projectID, Origin: session.OriginUI, UserID: creator}); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := saveGroupMeta(id, func(m *session.Meta) {
		// A room is nobody's agent chat, even in a project an agent owns.
		m.AgentID = ""
		m.Label = "Room · " + tk.Title
		m.TitleCustom = true
		m.TicketRoom = &session.TicketRoom{TicketID: tk.ID, Members: []session.RoomMember{home}}
	}); err != nil {
		_ = globalMgr.DeleteSession(c.Context(), id)
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := ticket.AttachSession(globalLayout, projectID, tk.ID, id); err != nil {
		_ = globalMgr.DeleteSession(c.Context(), id)
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeSessionTicketPointer(id, tk.ID)
	emitSystemEvent(id, store.KindTicketRoomOpened, fmt.Sprintf("Room opened in %s · the ticket stays in this project", roomHomePeer(projectID).Name),
		map[string]string{"ticket_id": tk.ID, "project_id": projectID})
	sess, _ := globalMgr.Registry().Session(id)
	c.JSON(http.StatusCreated, roomItem(c.Context(), sess, creator))
}

// roomOf loads {ticketID}'s room for a change; false = an error was written.
func roomOf(c *tool.Ctx) (session.Session, bool) {
	projectID, tk, ok := roomTicketFor(c)
	if !ok {
		return session.Session{}, false
	}
	sess, found := findTicketRoom(projectID, tk)
	if !found {
		c.JSON(http.StatusNotFound, map[string]string{"error": "this ticket has no room"})
		return session.Session{}, false
	}
	return sess, true
}

// apiTicketRoomUpdate handles PATCH /api/tickets/{ticketID}/room
// {share_full}: the caller's own entry, nobody else's.
func apiTicketRoomUpdate(c *tool.Ctx) {
	sess, ok := roomOf(c)
	if !ok {
		return
	}
	var req struct {
		ShareFull *bool `json:"share_full"`
	}
	if err := c.BindJSON(&req); err != nil || req.ShareFull == nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "share_full is required"})
		return
	}
	caller := actorID(c)
	if err := saveGroupMeta(sess.ID, func(m *session.Meta) {
		if m.TicketRoom == nil {
			return
		}
		if m.TicketRoom.ShareFull == nil {
			m.TicketRoom.ShareFull = map[string]bool{}
		}
		if *req.ShareFull {
			m.TicketRoom.ShareFull[caller] = true
		} else {
			delete(m.TicketRoom.ShareFull, caller)
		}
	}); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	sess, _ = globalMgr.Registry().Session(sess.ID)
	c.JSON(http.StatusOK, roomItem(c.Context(), sess, caller))
}

// apiTicketRoomAddMembers handles POST /api/tickets/{ticketID}/room/members
// {agent_ids}: the caller's own agents join, working for the caller.
func apiTicketRoomAddMembers(c *tool.Ctx) {
	sess, ok := roomOf(c)
	if !ok {
		return
	}
	var req struct {
		AgentIDs []string `json:"agent_ids"`
	}
	if err := c.BindJSON(&req); err != nil || len(req.AgentIDs) == 0 {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "agent_ids is required"})
		return
	}
	caller := actorID(c)
	_, own := ownTeamPeers(c.Context(), caller)
	var added []teamlink.Peer
	var addErr error
	if err := saveGroupMeta(sess.ID, func(m *session.Meta) {
		if m.TicketRoom != nil {
			added, addErr = roomAddMembers(m.TicketRoom, req.AgentIDs, own, caller)
		}
	}); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if addErr != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": addErr.Error()})
		return
	}
	announceRoomJoin(c, sess.ID, added)
	sess, _ = globalMgr.Registry().Session(sess.ID)
	c.JSON(http.StatusOK, roomItem(c.Context(), sess, caller))
}

// apiTicketRoomRemoveMember handles DELETE
// /api/tickets/{ticketID}/room/members/{agentID}: only the caller's own
// agent, never the home member. Its access to the ticket ends at once
// (team.RoomTicket); its past replies stay.
func apiTicketRoomRemoveMember(c *tool.Ctx) {
	sess, ok := roomOf(c)
	if !ok {
		return
	}
	caller, agentID := actorID(c), c.PathValue("agentID")
	var rmErr error
	if err := saveGroupMeta(sess.ID, func(m *session.Meta) {
		if m.TicketRoom != nil {
			rmErr = roomRemoveMember(m.TicketRoom, agentID, caller)
		}
	}); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if rmErr != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": rmErr.Error()})
		return
	}
	handle := agentID
	if globalTeam != nil {
		if a, err := globalTeam.Get(c.Context(), agentID); err == nil {
			handle = a.Handle
		}
	}
	by := actorName(c)
	ticketID := ""
	if sess.Meta.TicketRoom != nil {
		ticketID = sess.Meta.TicketRoom.TicketID
	}
	emitSystemEvent(sess.ID, store.KindGroupMemberRemoved, fmt.Sprintf("@%s left · its access to %s ended", handle, ticketID),
		map[string]string{"agent_id": agentID, "handle": handle, "owner_id": caller, "by": by})
	sess, _ = globalMgr.Registry().Session(sess.ID)
	c.JSON(http.StatusOK, roomItem(c.Context(), sess, caller))
}

func announceRoomJoin(c *tool.Ctx, roomID string, added []teamlink.Peer) {
	by := actorName(c)
	for _, p := range added {
		emitSystemEvent(roomID, store.KindGroupMemberAdded, fmt.Sprintf("@%s joined the room · by %s", p.Handle, by),
			map[string]string{"agent_id": p.ID, "handle": p.Handle, "owner_id": actorID(c), "by": by})
	}
}

/* ── sending ─────────────────────────────────────────────────────────── */

// sendRoomMessage is POST /sessions/{id}/send on a ticket room. The
// session gate already checked the caller may open the room (its project).
// The message is written into the thread; the sender's agents it names
// — joining the room first when not in it yet — or else the sender's
// default responder answer in the background.
func sendRoomMessage(c *tool.Ctx, sess session.Session, text string) {
	if strings.TrimSpace(text) == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "text required"})
		return
	}
	tk, err := ticket.Load(globalLayout, sess.Meta.ProjectID, sess.Meta.TicketRoom.TicketID)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "ticket not found"})
		return
	}
	sender := actorID(c)
	_, own := ownTeamPeers(c.Context(), sender)
	home := roomHomePeer(sess.Meta.ProjectID)
	plan, err := planRoomMessage(text, *sess.Meta.TicketRoom, sender, own, home)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Naming one of your own agents is how it joins.
	var added []teamlink.Peer
	if len(plan.Join) > 0 {
		var addErr error
		if err := saveGroupMeta(sess.ID, func(m *session.Meta) {
			if m.TicketRoom != nil {
				added, addErr = roomAddMembers(m.TicketRoom, plan.Join, own, sender)
			}
		}); err != nil || addErr != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": errors.Join(err, addErr).Error()})
			return
		}
		sess, _ = globalMgr.Registry().Session(sess.ID)
	}
	tr := sess.Meta.TicketRoom
	peers, targets, foreign, unknown, refused, noDefault := plan.Peers, plan.Targets, plan.Foreign, plan.Unknown, plan.Refused, plan.NoDefault

	// Backing sessions only for a message that runs someone: naming only
	// agents that cannot run creates nothing.
	backing := map[string]string{}
	for _, p := range peers {
		if len(targets) == 0 || p.Disabled {
			continue
		}
		var id string
		var err error
		if p.ID == roomHomePeerID {
			id, err = roomDefaultBacking(c, sess.ID, sess.Meta.ProjectID)
		} else {
			id, err = groupBacking(c, sess.ID, p.ID)
		}
		if err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		backing[p.ID] = id
	}

	now := time.Now().UTC()
	turn := store.ConversationTurn{
		TurnID: fmt.Sprintf("%d", now.UnixNano()), Timestamp: now, Role: "user", Source: "ui", Text: text,
		Sender: &store.Sender{ID: sender, Name: actorName(c), Channel: "ui", WickUserID: sender},
	}
	if err := recordGroupTurn(sess.ID, turn); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	announceRoomJoin(c, sess.ID, added)
	rc := newRoomCards()
	for _, f := range foreign {
		emitSystemEvent(sess.ID, store.KindMentionRefused, fmt.Sprintf("%s · @%s only takes instructions from @%s. Ask them, or %s.", foreignLabel(f, roomForeignAgent(c.Context(), tr, f, rc)), f.Owner, f.Owner, ownCaptainHint(own)),
			map[string]string{"from": "user", "to": f.Owner + "/" + f.Handle, "reason": "another person's agent"})
	}
	for _, h := range unknown {
		emitSystemEvent(sess.ID, store.KindMentionRefused, fmt.Sprintf("@%s isn't one of your agents", h),
			map[string]string{"from": "user", "to": h, "reason": "not your agent"})
	}
	for _, h := range refused {
		emitSystemEvent(sess.ID, store.KindMentionRefused, fmt.Sprintf("@%s is disabled", h),
			map[string]string{"from": "user", "to": h, "reason": "disabled"})
	}
	if noDefault {
		if h, ok := tr.Home(); ok && h.Kind == session.RoomMemberAgent {
			owner := personHandle(c.Context(), h.OwnerID)
			emitSystemEvent(sess.ID, store.KindMentionRefused, fmt.Sprintf("The home agent only takes instructions from @%s — mention one of your agents", owner),
				map[string]string{"from": "user", "reason": "another person's agent"})
		}
	}
	if len(targets) > 0 {
		run := newRoomRun(c.Context(), sess, tk, sender, peers, backing)
		go run.play(context.WithoutCancel(c.Context()), sess.ID, targets)
	}
	c.JSON(http.StatusOK, map[string]string{"status": "sent"})
}

// newRoomRun is the group runner set up for one person's message in a
// room: sender's members only, the room's hop cap over them, and the
// prompt built for sender.
func newRoomRun(ctx context.Context, sess session.Session, tk ticket.Ticket, sender string, peers []teamlink.Peer, backing map[string]string) *groupRun {
	roomID := sess.ID
	shareFull := sess.Meta.TicketRoom.ShareFull[sender]
	handles := map[string]string{}
	handleOf := func(uid string) string {
		if h, ok := handles[uid]; ok {
			return h
		}
		h := personHandle(ctx, uid)
		handles[uid] = h
		return h
	}
	return &groupRun{
		group: session.AgentGroup{Name: tk.Title}, members: peers, limit: teamlink.GroupLimit(peers, 0), backing: backing,
		turn: func(ctx context.Context, p teamlink.Peer, prompt string) (string, error) {
			// A queued turn re-reads the room: a member removed since the
			// message was sent does not run (nor spend its owner's budget).
			if !roomStillHas(roomID, p, sender) {
				return "", errLeftRoom
			}
			return runBackingTurn(ctx, backing[p.ID], prompt)
		},
		history: func() []store.ConversationTurn {
			turns, _ := loadConversation(globalLayout, roomID)
			return turns
		},
		record: func(t store.ConversationTurn) {
			// Whom the speaker works for travels with its turn: everyone's
			// Captain is "captain", the owner tells them apart.
			if t.Speaker != nil {
				t.Speaker.OwnerID = sender
			}
			if t.Role == "system" {
				recordSystemTurn(globalLayout, globalBcast, roomID, t)
				return
			}
			if err := recordGroupTurn(roomID, t); err != nil {
				log.Warn().Err(err).Str("session", roomID).Msg("team: room turn write failed")
			}
		},
		typing: func(p teamlink.Peer, on bool) {
			state := "stop"
			if on {
				state = "start"
			}
			body, _ := json.Marshal(map[string]string{"agent_id": p.ID, "handle": p.Handle, "owner_id": sender, "state": state, "session_id": backing[p.ID]})
			if globalBcast != nil {
				globalBcast.PublishRaw(roomID, "", evGroupTyping, string(body))
			}
		},
		prompt: func(self teamlink.Peer, from string, history []store.ConversationTurn) string {
			return roomPrompt(roomPromptIn{
				Ticket: tk, Self: self, From: from, Sender: sender, ShareFull: shareFull, Peers: peers, History: history,
				OwnerOf: roomTurnOwner, HandleOf: handleOf,
			})
		},
	}
}

// roomStillHas reports whether p is still in room roomID for sender.
func roomStillHas(roomID string, p teamlink.Peer, sender string) bool {
	room, ok := globalMgr.Registry().Session(roomID)
	if !ok || room.Meta.TicketRoom == nil {
		return false
	}
	tr := room.Meta.TicketRoom
	if p.ID == roomHomePeerID {
		h, has := tr.Home()
		return has && h.Kind == session.RoomMemberProjectDefault
	}
	_, in := tr.Member(p.ID, sender)
	return in
}

// roomTurnOwner is whose a room turn is: its writer, or the person the
// agent that wrote it works for (its backing session's owner).
func roomTurnOwner(t store.ConversationTurn) string {
	if t.Speaker != nil && t.Speaker.OwnerID != "" {
		return t.Speaker.OwnerID
	}
	if t.Speaker != nil {
		if s, ok := globalMgr.Registry().Session(t.Speaker.SessionID); ok {
			return s.Meta.UserID
		}
		return ""
	}
	if t.Sender != nil {
		if t.Sender.WickUserID != "" {
			return t.Sender.WickUserID
		}
		return t.Sender.ID
	}
	return ""
}

// roomDefaultBacking is the project default's backing session in room
// roomID for the caller: a plain session of the project, run as the
// caller, created on first use.
func roomDefaultBacking(c *tool.Ctx, roomID, projectID string) (string, error) {
	owner := actorID(c)
	for _, s := range globalMgr.Registry().Sessions() {
		if s.Meta.GroupSessionID == roomID && s.Meta.AgentID == "" && s.Meta.UserID == owner {
			return s.ID, nil
		}
	}
	prov, modelID := resolveSessionTarget(c, "", "", projectID)
	preset := "default"
	if proj, ok := globalMgr.Registry().Project(projectID); ok && proj.Meta.Defaults.Preset != "" {
		preset = proj.Meta.Defaults.Preset
	}
	id := uuid.New().String()
	if _, err := globalMgr.CreateSession(c.Context(), session.CreateOptions{ID: id, ProjectID: projectID, Origin: session.OriginUI, Preset: preset, UserID: owner}); err != nil {
		return "", err
	}
	if err := globalMgr.AddAgent(id, "main", prov); err != nil {
		return "", err
	}
	if modelID != "" {
		if err := session.SetModelID(globalLayout, id, "main", modelID); err != nil {
			log.Ctx(c.Context()).Warn().Msgf("ticket room: set model id: %s", err.Error())
		}
	}
	return id, saveGroupMeta(id, func(m *session.Meta) {
		// Never someone's agent chat, even in a project an agent owns.
		m.AgentID = ""
		m.GroupSessionID = roomID
	})
}
