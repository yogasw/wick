package teamlink

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// MaxRecentChats caps the recent chats of a teammate the caller's LLM is
// shown for an explicit resume.
const MaxRecentChats = 5

var (
	// ErrUnknownChat refuses a chat id that is not one of the target's
	// chats with the caller's side.
	ErrUnknownChat = errors.New("unknown chat — pass a session_id that team chats listed for this teammate, or omit chat")
	// ErrChatAndNewChat refuses chat and new_chat together.
	ErrChatAndNewChat = errors.New("pass chat or new_chat, not both")
)

// ChatInfo is one chat of a Team agent as the caller's LLM sees it.
type ChatInfo struct {
	SessionID  string    `json:"session_id"`
	Title      string    `json:"title,omitempty"`
	LastActive time.Time `json:"last_active,omitempty"`
	Main       bool      `json:"main,omitempty"`
	// SlackThread links the chat's Slack thread when the agent is a Slack
	// remote, so the pair is unambiguous on both sides.
	SlackThread string `json:"slack_thread,omitempty"`
	// Current marks the chat paired with the asking conversation.
	Current bool `json:"current,omitempty"`
	// UserID is whose chat it is and LinkedFrom the conversation it is
	// paired with; both stay server-side.
	UserID     string `json:"-"`
	LinkedFrom string `json:"-"`
	// AgentID is whose chat it is (PairedLister).
	AgentID string `json:"-"`
}

// ChatLister is optionally implemented by a ChatLinker: agent's chats for
// its chat user, newest first.
type ChatLister interface {
	Chats(ctx context.Context, agent Peer) []ChatInfo
}

// PairedLister is optionally implemented by a ChatLister: every chat of
// any agent paired with callerSession, in one pass over the sessions, so
// LinkedChats costs one scan rather than one per teammate — and nothing
// more (no directory read) when the conversation has no pairing.
type PairedLister interface {
	PairedWith(ctx context.Context, callerSession string) []ChatInfo
}

// TeamChat is a teammate with the chat paired with the asking
// conversation (Chat) and, on request, its recent chats.
type TeamChat struct {
	AgentID string     `json:"agent_id"`
	Handle  string     `json:"handle"`
	Chat    *ChatInfo  `json:"chat,omitempty"`
	Recent  []ChatInfo `json:"recent,omitempty"`
}

// chatUser is whose chats with p a turn runs in: a recipient's for an
// agent shared with them, else the owner's.
func (p Peer) chatUser() string {
	if p.ChatUser != "" {
		return p.ChatUser
	}
	return p.OwnerID
}

// ownChats is p's chats for its chat user, newest first. Only that user's
// own chats ever pass, whatever the lister returns: another person's chat
// with a shared agent — readable or not under the share's history toggle
// — is never offered for pairing or resume.
func (h *Hub) ownChats(ctx context.Context, p Peer) []ChatInfo {
	ls, ok := h.Turns.(ChatLister)
	if !ok {
		return nil
	}
	user := p.chatUser()
	var out []ChatInfo
	for _, c := range ls.Chats(ctx, p) {
		if c.SessionID != "" && user != "" && c.UserID == user {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastActive.After(out[j].LastActive) })
	return out
}

// chatView is chat id of p as the caller sees it, nil for "".
func (h *Hub) chatView(ctx context.Context, p Peer, id string) *ChatInfo {
	if id == "" {
		return nil
	}
	for _, c := range h.ownChats(ctx, p) {
		if c.SessionID == id {
			return &c
		}
	}
	return &ChatInfo{SessionID: id}
}

// ownsChat reports whether id is one of p's chats for its chat user.
func (h *Hub) ownsChat(ctx context.Context, p Peer, id string) bool {
	for _, c := range h.ownChats(ctx, p) {
		if c.SessionID == id {
			return true
		}
	}
	return false
}

// resumeScope maps a teammate the caller reaches (a plain send's scope)
// to the peer whose chats a resume — and the chat lists — may use in a
// session of sessionUser: the teammate itself in the owner's own session;
// in a recipient's session, the teammate as shared with that recipient
// (ChatUser = recipient), false when it is not, so another person's chats
// are never offered. SharedPeers is read once per call.
func (h *Hub) resumeScope(ctx context.Context, sessionUser, ownerID string) func(Peer) (Peer, bool) {
	if sessionUser == "" || sessionUser == ownerID {
		return func(p Peer) (Peer, bool) { return p, true }
	}
	shared := map[string]Peer{}
	if sd, ok := h.Dir.(SharedDirectory); ok {
		if all, err := sd.SharedPeers(ctx, sessionUser); err == nil {
			for _, p := range all {
				if !p.Disabled {
					p.ChatUser = sessionUser
					shared[p.ID] = p
				}
			}
		}
	}
	return func(p Peer) (Peer, bool) {
		if p.chatUser() == sessionUser {
			return p, true
		}
		sp, ok := shared[p.ID]
		return sp, ok
	}
}

// chatTargets is the teammates a chat list covers: the caller agent's
// plain-send reach (Reachable), each mapped through resumeScope.
func (h *Hub) chatTargets(ctx context.Context, callerAgentID, sessionUser string) (peers []Peer, scope func(Peer) (Peer, bool), err error) {
	self, err := h.Dir.Get(ctx, callerAgentID)
	if err != nil {
		return nil, nil, err
	}
	if peers, err = h.reachableFor(ctx, self.OwnerID, self.ID); err != nil {
		return nil, nil, err
	}
	return peers, h.resumeScope(ctx, sessionUser, self.OwnerID), nil
}

// LinkedChats is, for every teammate the caller agent can reach (a plain
// send's scope), the chat paired with callerSession that belongs to the
// session's person (resumeScope) — the caller's own pairings only.
// Teammates with no such chat are left out.
func (h *Hub) LinkedChats(ctx context.Context, callerAgentID, callerSession, sessionUser string) ([]TeamChat, error) {
	if callerAgentID == "" || callerSession == "" {
		return nil, nil
	}
	if _, ok := h.Turns.(ChatLister); !ok {
		return nil, nil
	}
	pl, indexed := h.Turns.(PairedLister)
	var paired []ChatInfo
	if indexed {
		if paired = pl.PairedWith(ctx, callerSession); len(paired) == 0 {
			return nil, nil
		}
	}
	reach, scope, err := h.chatTargets(ctx, callerAgentID, sessionUser)
	if err != nil {
		return nil, err
	}
	var out []TeamChat
	for _, rp := range reach {
		p, ok := scope(rp)
		if !ok {
			continue
		}
		if indexed {
			// Same filter as ownChats: the chat user's own chat only.
			for _, c := range paired {
				if c.AgentID == p.ID && c.UserID != "" && c.UserID == p.chatUser() && c.LinkedFrom == callerSession {
					c.Current = true
					out = append(out, TeamChat{AgentID: p.ID, Handle: p.Handle, Chat: &c})
					break
				}
			}
			continue
		}
		for _, c := range h.ownChats(ctx, p) {
			if c.LinkedFrom == callerSession {
				c.Current = true
				out = append(out, TeamChat{AgentID: p.ID, Handle: p.Handle, Chat: &c})
				break
			}
		}
	}
	return out, nil
}

// RecentChats is teammate to's chat paired with callerSession plus its
// most recent chats for the session's person (MaxRecentChats), for a
// resume the person asks for explicitly. to is looked up in a plain
// send's scope; a teammate the person has no chats with answers empty.
func (h *Hub) RecentChats(ctx context.Context, callerAgentID, callerSession, sessionUser, to string) (TeamChat, error) {
	if callerAgentID == "" {
		return TeamChat{}, ErrNotTeamSession
	}
	peers, scope, err := h.chatTargets(ctx, callerAgentID, sessionUser)
	if err != nil {
		return TeamChat{}, err
	}
	want := NormalizeHandle(to)
	for _, p := range peers {
		if p.Handle != want {
			continue
		}
		tc := TeamChat{AgentID: p.ID, Handle: p.Handle}
		sp, ok := scope(p)
		if !ok {
			return tc, nil
		}
		for i, c := range h.ownChats(ctx, sp) {
			if callerSession != "" && c.LinkedFrom == callerSession {
				c.Current = true
				cur := c
				tc.Chat = &cur
			}
			if i < MaxRecentChats {
				tc.Recent = append(tc.Recent, c)
			}
		}
		return tc, nil
	}
	return TeamChat{}, ErrUnknownHandle
}

// FormatLinkedChats is the "This session" lines naming the chat paired
// with this conversation at each teammate, "" for none. Kept short: it is
// in every spawn's prompt.
func FormatLinkedChats(chats []TeamChat) string {
	var b strings.Builder
	for _, tc := range chats {
		if tc.Chat == nil {
			continue
		}
		fmt.Fprintf(&b, "\n- @%s → session %s", tc.Handle, tc.Chat.SessionID)
		if tc.Chat.Title != "" {
			fmt.Fprintf(&b, " %q", tc.Chat.Title)
		}
		if tc.Chat.SlackThread != "" {
			b.WriteString(" slack: " + tc.Chat.SlackThread)
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "linked_team_chats (your chat at each teammate for THIS conversation; team messages land there):" +
		b.String() +
		"\nNever move to an older chat on your own: pass chat=<session_id> only when the user asks to continue one."
}
