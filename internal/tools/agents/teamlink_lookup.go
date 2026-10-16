package agents

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/internal/entity"
)

// ThreadSession is one session session_lookup reports for a Slack thread.
type ThreadSession struct {
	SessionID  string    `json:"session_id"`
	Agent      string    `json:"agent"`
	ProjectID  string    `json:"project_id,omitempty"`
	TicketID   string    `json:"ticket_id,omitempty"`
	OwnerUser  string    `json:"owner_user,omitempty"`
	LastActive time.Time `json:"last_active"`
	CanSend    bool      `json:"can_send"`
	Reason     string    `json:"reason,omitempty"`
	Chat       string    `json:"chat,omitempty"`
}

// ThreadLookup is session_lookup's answer: the sessions the caller may
// see, newest first, and how many others answer in the thread (Hidden).
type ThreadLookup struct {
	Channel  string          `json:"channel"`
	ThreadTS string          `json:"thread_ts"`
	Sessions []ThreadSession `json:"sessions"`
	Hidden   int             `json:"hidden"`
}

// TeamSessionLookup lists the sessions bound to the Slack thread
// (channel, threadTS) — Meta.ChannelRef in the session registry, so it
// needs no running Slack bot — as callerAgent asking from callerSession
// sees them. A session shows when its Team agent (teamAgentOfSession) is
// one of the caller owner's agents or shared with that owner, or, with no
// agent, when it is the owner's own; every other one only adds to Hidden.
// can_send is what a message to=@agent chat=<session> would get: the
// target's CanTarget, and for the caller's own agent only within one
// ticket (chatAllowed self).
func TeamSessionLookup(ctx context.Context, callerAgent, callerSession, channel, threadTS string) (any, error) {
	if globalTeam == nil || globalMgr == nil {
		return nil, errors.New("team messaging is not configured on this server")
	}
	caller, err := globalTeam.Get(ctx, callerAgent)
	if err != nil {
		return nil, err
	}
	owner := caller.OwnerUserID
	out := ThreadLookup{Channel: channel, ThreadTS: threadTS, Sessions: []ThreadSession{}}
	dir := teamDirectory{svc: globalTeam}
	for id, s := range globalMgr.Registry().Sessions() {
		r := s.Meta.ChannelRef
		if r == nil || r.Channel != "slack" || r.ChatID != channel || r.ThreadID != threadTS {
			continue
		}
		m := s.Meta
		row := ThreadSession{SessionID: id, ProjectID: m.ProjectID, OwnerUser: m.UserID, LastActive: m.LastActive}
		if m.ProjectID != "" {
			if tk, ok := ticket.FindBySession(globalLayout, m.ProjectID, id); ok {
				row.TicketID = tk.ID
			}
		}
		agentID := teamAgentOfSession(ctx, id)
		if agentID == "" {
			if m.UserID == "" || m.UserID != owner {
				out.Hidden++
				continue
			}
			row.Reason = "no Team agent answers in this session"
			out.Sessions = append(out.Sessions, row)
			continue
		}
		p, err := globalTeam.Get(ctx, agentID)
		if err != nil {
			out.Hidden++
			continue
		}
		peer := dir.peer(p)
		if p.OwnerUserID != owner {
			if _, ok := sharedWithUser(ctx, p, owner); !ok {
				out.Hidden++
				continue
			}
			peer.ChatUser = owner
		}
		row.Agent = p.Handle
		if err := lookupCanSend(ctx, peer, p, callerAgent, callerSession, id); err != nil {
			row.Reason = err.Error()
		} else {
			row.CanSend, row.Chat = true, id
		}
		out.Sessions = append(out.Sessions, row)
	}
	slices.SortFunc(out.Sessions, func(a, b ThreadSession) int {
		if c := b.LastActive.Compare(a.LastActive); c != 0 {
			return c
		}
		return strings.Compare(a.SessionID, b.SessionID)
	})
	return out, nil
}

// lookupCanSend is nil when callerAgent, asking from callerSession, may
// message target's session chat with chat=: the checks Hub.chatAllowed
// runs for a chat that is not one of target's paired chats.
func lookupCanSend(ctx context.Context, target teamlink.Peer, p entity.AgentPersona, callerAgent, callerSession, chat string) error {
	if p.Disabled {
		return errors.New("agent @" + p.Handle + " is disabled")
	}
	self := target.ID == callerAgent
	if self && chat == callerSession {
		return errors.New("this is the session you are in")
	}
	ticketMatch, err := poolTurns{}.CanTarget(ctx, target, callerSession, chat)
	if err != nil {
		return err
	}
	if self && !ticketMatch {
		return teamlink.ErrSelfChatTicket
	}
	return nil
}
