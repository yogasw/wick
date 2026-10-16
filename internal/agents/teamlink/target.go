package teamlink

import (
	"context"
	"errors"
	"fmt"
)

// ErrSelfChatTicket refuses a message from an agent to another chat of
// its own that is not in the same ticket as the asking chat.
var ErrSelfChatTicket = errors.New("you can message another chat of yours only when both chats are in the same ticket")

// SessionTargeter is optionally implemented by Turns: whether chat — a
// session that is not one of target's own chats (ownsChat), such as a
// Slack thread session of target's project — may still be addressed with
// chat=. nil admits it when the session exists, belongs to target, and is
// either its chat user's or in the same ticket as callerSession; ticket
// reports that last case. A session that does not exist is an error,
// never a reason to open one.
type SessionTargeter interface {
	CanTarget(ctx context.Context, target Peer, callerSession, chat string) (ticket bool, err error)
}

// HoldChecker is optionally implemented by Turns: whether a person took
// sessionID over (take over), so a Team turn another agent asks for there
// must not run. nil holds nothing.
type HoldChecker interface {
	Held(ctx context.Context, sessionID string) bool
}

// SessionLabeler is optionally implemented by Turns: how a session reads
// to an agent that gets a message from it — its id, and its Slack thread
// when it has one.
type SessionLabeler interface {
	SessionLabel(ctx context.Context, sessionID string) string
}

// chatAllowed checks chat as the chat= of a message to target from
// callerSession; self is a message from target to itself. One of target's
// own chats (not for self) is paired with callerSession (link); a chat
// SessionTargeter admits is used as is, never paired, and for self only
// within one ticket.
func (h *Hub) chatAllowed(ctx context.Context, target Peer, callerSession, chat string, self bool) (link bool, err error) {
	if self && chat == callerSession {
		return false, ErrSelf
	}
	if !self && h.ownsChat(ctx, target, chat) {
		return true, nil
	}
	st, ok := h.Turns.(SessionTargeter)
	if !ok {
		return false, ErrUnknownChat
	}
	ticket, err := st.CanTarget(ctx, target, callerSession, chat)
	if err != nil {
		return false, err
	}
	if self && !ticket {
		return false, ErrSelfChatTicket
	}
	return false, nil
}

// frameSelf is how a message from another chat of the same agent reads:
// the chat it came from (sessionLabel), so the receiving chat knows which
// conversation asked.
func (h *Hub) frameSelf(ctx context.Context, from Peer, callerSession, text string) string {
	origin := "session " + callerSession
	if sl, ok := h.Turns.(SessionLabeler); ok {
		if l := sl.SessionLabel(ctx, callerSession); l != "" {
			origin = l
		}
	}
	return fmt.Sprintf("Message from your own other chat (%s), as %s:\n%s", origin, from.Label(), text)
}

// ReadableChat is whether callerAgentID, asking from callerSession (its
// wick user sessionUser), may read chat — a session of the agent to
// names — with the team read_chat op: exactly when a message to=to
// chat=chat from there would be let in (Send's resolution and checks,
// then chatAllowed). It reads nothing and pairs nothing; it returns the
// target as resolved.
func (h *Hub) ReadableChat(ctx context.Context, callerAgentID, callerSession, sessionUser, to, chat string) (Peer, error) {
	if callerAgentID == "" {
		return Peer{}, ErrNotTeamSession
	}
	caller, err := h.Dir.Get(ctx, callerAgentID)
	if err != nil {
		return Peer{}, ErrNotTeamSession
	}
	if chat == "" {
		return Peer{}, errors.New("pass chat: the session_id to read")
	}
	_, target, err := h.Resolve(ctx, caller.OwnerID, to)
	if err != nil {
		return Peer{}, err
	}
	self := target.ID == caller.ID
	if self && (target.Remote || chat == callerSession) {
		return Peer{}, ErrSelf
	}
	if target.OwnerID != caller.OwnerID && target.ChatUser != caller.OwnerID {
		return Peer{}, ErrSharedHumanOnly
	}
	if !self && !target.AcceptsFrom(caller) {
		return Peer{}, ErrMentionsOff
	}
	rp, ok := target, true
	if !self {
		rp, ok = h.resumeScope(ctx, sessionUser, caller.OwnerID)(target)
	}
	if !ok {
		return Peer{}, ErrUnknownChat
	}
	if _, err := h.chatAllowed(ctx, rp, callerSession, chat, self); err != nil {
		return Peer{}, err
	}
	return rp, nil
}
