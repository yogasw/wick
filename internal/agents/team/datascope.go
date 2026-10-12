package team

import (
	"context"
	"errors"
	"fmt"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/internal/connectors"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// Data scope: which sessions and projects wick's own per-session data
// (notes, tickets, schedules) may be read or written in. It sits on top of
// the connector level a Scope resolves:
//
//   - never across users: a person reaches a session they own (or any,
//     when admin / allowed to see all sessions) and a project
//     project.CanAccess lets them see — the same rules the schedule and
//     title tools use;
//   - an ordinary agent reaches only its own sessions (and the sub-agent
//     sessions under them) and its own project;
//   - the Captain reaches every session and project of its owner.
//
// Errors read "not found" so a caller cannot probe what exists. Inside an
// agent session a call with no person behind it (callerUID "") fails
// closed.

// personOf is the signed-in user on ctx when it is the person the call
// runs for; nil otherwise (the agent's internal identity, or none).
func personOf(ctx context.Context, callerUID string) *entity.User {
	if u := login.GetUser(ctx); u != nil && u.ID == callerUID {
		return u
	}
	return nil
}

// InAgent reports whether ctx is an agent session's call.
func InAgent(ctx context.Context) bool { return agentScopeFrom(ctx) != nil }

// AgentOwner is the owner of the agent whose session ctx belongs to; ok is
// false outside an agent session.
func AgentOwner(ctx context.Context) (string, bool) {
	s := agentScopeFrom(ctx)
	if s == nil {
		return "", false
	}
	return s.ownerID, true
}

// seesAllSessions mirrors canManageSession's bypass. It never applies
// inside an agent session: an agent — the Captain included — stays within
// its owner's own data however wide the owner's own rights are.
func seesAllSessions(ctx context.Context, u *entity.User) bool {
	return u != nil && agentScopeFrom(ctx) == nil && (u.IsAdmin() || u.CanSeeAllSessions())
}

// projectAccessOf is the project.Access of the person the call runs for.
func projectAccessOf(ctx context.Context, callerUID string) project.Access {
	if owner, ok := AgentOwner(ctx); ok {
		// An agent sees projects as its owner would without any admin
		// bypass: owned, tag-shared, or shared with everyone.
		acc := project.Access{UserID: owner}
		if u := login.GetUser(ctx); u != nil && u.ID == owner {
			acc.TagIDs = login.GetUserTagIDs(ctx)
		}
		return acc
	}
	u := personOf(ctx, callerUID)
	acc := project.Access{UserID: callerUID, IsAdmin: seesAllSessions(ctx, u)}
	if u != nil {
		acc.TagIDs = login.GetUserTagIDs(ctx)
	}
	return acc
}

// agentNeedsCaller fails an agent call with no person behind it.
func agentNeedsCaller(ctx context.Context, callerUID string, notFound error) error {
	if callerUID == "" && agentScopeFrom(ctx) != nil {
		return notFound
	}
	return nil
}

// AgentID is the agent the scope was built for ("" for DenyAll).
func (s *Scope) AgentID() string { return s.agentID }

// Captain reports whether the scope belongs to the owner's Captain.
func (s *Scope) Captain() bool { return s.captain }

// agentScopeFrom returns the Team scope on ctx, nil outside an agent.
func agentScopeFrom(ctx context.Context) *Scope {
	s, _ := connectors.AgentScopeFrom(ctx).(*Scope)
	return s
}

// CheckSessionTarget allows callingSID to act on targetSID's data.
// callerUID is the person the call runs for ("" = unknown, no user check).
func CheckSessionTarget(ctx context.Context, layout config.Layout, callerUID, callingSID, targetSID string) error {
	if targetSID == "" || targetSID == callingSID {
		return nil
	}
	notFound := fmt.Errorf("session not found: %s", targetSID)
	if InTicketRoom(layout, callingSID) {
		return notFound
	}
	sess, err := session.Load(layout, targetSID)
	if err != nil {
		return notFound
	}
	if err := agentNeedsCaller(ctx, callerUID, notFound); err != nil {
		return err
	}
	if callerUID != "" && !seesAllSessions(ctx, personOf(ctx, callerUID)) && sess.Meta.UserID != callerUID {
		return notFound
	}
	return checkAgentSession(ctx, layout, targetSID, notFound)
}

// CheckAgentSession is the agent half of CheckSessionTarget, for callers
// that already enforce the per-user rule themselves (schedules).
func CheckAgentSession(ctx context.Context, layout config.Layout, targetSID string) error {
	return checkAgentSession(ctx, layout, targetSID, fmt.Errorf("session not found: %s", targetSID))
}

func checkAgentSession(ctx context.Context, layout config.Layout, targetSID string, notFound error) error {
	s := agentScopeFrom(ctx)
	if s == nil {
		return nil
	}
	if s.captain {
		// The Captain reaches its owner's sessions only — never another
		// user's, whatever admin rights the owner has as a person.
		sess, err := session.Load(layout, targetSID)
		if err != nil || s.ownerID == "" || sess.Meta.UserID != s.ownerID {
			return notFound
		}
		return nil
	}
	if s.denyAll || s.agentID == "" {
		return notFound
	}
	agent, err := AgentOfSession(layout, targetSID)
	if err != nil || agent != s.agentID {
		return notFound
	}
	return nil
}

// CheckProjectTarget allows callingSID to act on projectID's data. The
// calling session's own project always passes.
func CheckProjectTarget(ctx context.Context, layout config.Layout, callerUID, callingSID, projectID string) error {
	if projectID == "" {
		return nil
	}
	notFound := fmt.Errorf("project not found: %s", projectID)
	if callingSID != "" {
		// A ticket room's session reaches one ticket, through RoomTicket at
		// the caller; never the project it happens to run in.
		if sess, err := session.Load(layout, callingSID); err == nil {
			if SessionInTicketRoom(layout, sess) {
				return notFound
			}
			if sess.Meta.ProjectID == projectID {
				return nil
			}
		}
	}
	p, err := project.Load(layout, projectID)
	if err != nil {
		return notFound
	}
	if err := agentNeedsCaller(ctx, callerUID, notFound); err != nil {
		return err
	}
	if callerUID != "" && !project.CanAccess(p.Meta, projectAccessOf(ctx, callerUID)) {
		return notFound
	}
	return CheckAgentProject(ctx, projectID)
}

// RoomTicket is the one ticket a ticket room's member may reach from its
// backing session (callingSID): the room's ticket, in the room's project.
// ok only while the session is the backing session of a current member
// — the agent and the person it works for both still listed — that
// person may open the project, and the ticket still exists with the room
// on it. Removing the member, losing the project, deleting the ticket or
// moving the room off it ends it on the next call; nothing is cached.
func RoomTicket(ctx context.Context, layout config.Layout, callingSID string) (projectID, ticketID string, ok bool) {
	if callingSID == "" {
		return "", "", false
	}
	sess, err := session.Load(layout, callingSID)
	if err != nil || sess.Meta.GroupSessionID == "" {
		return "", "", false
	}
	room, err := session.Load(layout, sess.Meta.GroupSessionID)
	if err != nil || room.Meta.TicketRoom == nil || room.Meta.ProjectID == "" {
		return "", "", false
	}
	tr := room.Meta.TicketRoom
	owner := sess.Meta.UserID
	if sess.Meta.AgentID != "" {
		if _, member := tr.Member(sess.Meta.AgentID, owner); !member {
			return "", "", false
		}
	} else if home, has := tr.Home(); !has || home.Kind != session.RoomMemberProjectDefault {
		// A backing session with no agent is the project default's.
		return "", "", false
	}
	p, err := project.Load(layout, room.Meta.ProjectID)
	if err != nil || owner == "" {
		return "", "", false
	}
	acc := project.Access{UserID: owner}
	if u := login.GetUser(ctx); u != nil && u.ID == owner {
		acc.TagIDs = login.GetUserTagIDs(ctx)
	}
	if !project.CanAccess(p.Meta, acc) {
		return "", "", false
	}
	// The ticket is the record: a room it no longer lists (moved, or the
	// ticket gone by any path) grants nothing.
	tk, err := ticket.Load(layout, room.Meta.ProjectID, tr.TicketID)
	if err != nil || !listsSession(tk, room.ID) {
		return "", "", false
	}
	return room.Meta.ProjectID, tr.TicketID, true
}

func listsSession(tk ticket.Ticket, sid string) bool {
	for _, s := range tk.Sessions {
		if s == sid {
			return true
		}
	}
	return false
}

// ErrRoomSession refuses filing a ticket room, or a member's backing
// session, onto or off a ticket: a room stays on the ticket it was opened
// for, and a backing session is not a chat to file anywhere.
var ErrRoomSession = errors.New("not supported in a ticket room")

// RefuseRoomSession is the guard every attach/detach path (connector, host
// API, HTTP) runs on the session it would move. An id with no session on
// disk is left to the ticket store, as before.
func RefuseRoomSession(layout config.Layout, sid string) error {
	sess, err := session.Load(layout, sid)
	if err != nil {
		return nil
	}
	if sess.Meta.TicketRoom != nil || sess.Meta.GroupSessionID != "" {
		return ErrRoomSession
	}
	return nil
}

// InTicketRoom reports whether sid answers in a ticket room — a member's
// backing session, or a sub-agent under one at any depth. Such a session
// reaches its room's ticket (RoomTicket) and nothing else: not the rest of
// the project it runs in, not other sessions.
func InTicketRoom(layout config.Layout, sid string) bool {
	if sid == "" {
		return false
	}
	sess, err := session.Load(layout, sid)
	if err != nil {
		return false
	}
	return SessionInTicketRoom(layout, sess)
}

// roomParentHops bounds the room walk far past any real delegation depth
// (an admin may raise that well above AgentOfSession's maxParentHops); it
// only stops a corrupt chain from spinning.
const roomParentHops = 1024

// SessionInTicketRoom is InTicketRoom for a session the caller already
// loaded. The parent chain is walked to its top; a chain that loops back
// on itself, or runs past roomParentHops, is refused as if it were in a
// room (fail closed). A parent that no longer exists ends the walk: there
// is no room left above it.
func SessionInTicketRoom(layout config.Layout, sess session.Session) bool {
	seen := map[string]bool{}
	for hops := 0; ; hops++ {
		if sess.Meta.GroupSessionID != "" {
			room, err := session.Load(layout, sess.Meta.GroupSessionID)
			return err == nil && room.Meta.TicketRoom != nil
		}
		parent := sess.Meta.ParentSessionID
		if parent == "" {
			return false
		}
		seen[sess.ID] = true
		if seen[parent] || hops >= roomParentHops {
			return true
		}
		next, err := session.Load(layout, parent)
		if err != nil {
			return false
		}
		sess = next
	}
}

// CheckAgentProject refuses an ordinary agent any project but its own
// (the calling session's, which CheckProjectTarget already let through).
func CheckAgentProject(ctx context.Context, projectID string) error {
	s := agentScopeFrom(ctx)
	if s == nil || s.captain {
		return nil
	}
	return fmt.Errorf("project not found: %s", projectID)
}

// CheckOwnerRecipient refuses an agent session any notification recipient
// but the person the call runs for.
func CheckOwnerRecipient(ctx context.Context, callerUID, recipientUID string) error {
	if agentScopeFrom(ctx) == nil {
		return nil
	}
	if callerUID == "" || recipientUID != callerUID {
		return errors.New("an agent may only notify its owner — use send_to_me")
	}
	return nil
}
