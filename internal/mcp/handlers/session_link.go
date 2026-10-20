package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/sessionlink"
	"github.com/yogasw/wick/internal/login"
)

const sessionLinkDescription = "Linked sessions: other conversations handling the same matter as THIS one " +
	"(a card thread and its main thread, sessions on one ticket, a thread you opened from here, a teammate's chat you messaged). " +
	"action=list shows them with what each is doing now (state running/idle, or pending for a thread you opened that nobody answered yet) — " +
	"check it before starting work another session may already be doing. action=read session=<id> returns that session's latest turns, " +
	"to use its result instead of redoing it. action=link session=<id> why=<reason> ties another session to this one (both sides see it); " +
	"action=unlink session=<id> removes a wrong link. action=link_thread chat_id=<channel id> thread_ts=<ts of the message you posted> why=<reason> " +
	"right after you post a NEW thread somewhere (e.g. you asked another channel something on someone's request): the session a reply there starts " +
	"is linked to this one, so whoever answers there knows where it came from. A turn opens with a [linked] line while a linked session is working. " +
	"No session_id: this conversation is resolved from the call."

// SessionLinkDescriptor is the wick_session_link tool.
func SessionLinkDescriptor() ToolDescriptor {
	return ToolDescriptor{
		Name:        "wick_session_link",
		Description: sessionLinkDescription,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string", "enum": []string{"list", "read", "link", "unlink", "link_thread"},
					"description": "list | read | link | unlink | link_thread.",
				},
				"session":   map[string]any{"type": "string", "description": "read/link/unlink: the other session's id."},
				"why":       map[string]any{"type": "string", "description": "link/link_thread: what ties them together, shown to both sides."},
				"n":         map[string]any{"type": "integer", "description": "read: how many latest turns (default 6, max 20)."},
				"chat_id":   map[string]any{"type": "string", "description": "link_thread: the channel id the new thread is in (Slack C…)."},
				"thread_ts": map[string]any{"type": "string", "description": "link_thread: the ts of the message that starts the new thread."},
				"channel":   map[string]any{"type": "string", "description": "link_thread: chat adapter, default slack."},
			},
			"required": []string{"action"},
		},
		Annotations: &ToolAnnotation{
			Title:           "Linked sessions",
			ReadOnlyHint:    PtrBool(false),
			DestructiveHint: PtrBool(false),
		},
	}
}

// WickSessionLink handles wick_session_link for the call's own session.
func WickSessionLink(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, layout agentconfig.Layout, refreshSession func(id string) error, args map[string]any) {
	const tool = "wick_session_link"
	self := ResolveSessionPreferArg(SessionOf(r), "")
	if self == "" {
		rsp.ToolError(w, req.ID, "no session on the call", tool)
		return
	}
	user := login.GetUser(r.Context())
	own, err := session.Load(layout, self)
	if err != nil || !canManageSession(user, own.Meta.UserID) {
		rsp.ToolError(w, req.ID, "session not found: "+self, tool)
		return
	}
	str := func(k string) string { v, _ := args[k].(string); return strings.TrimSpace(v) }
	other := str("session")
	// visible: the other session exists and the caller may see it.
	visible := func(id string) bool {
		s, err := session.Load(layout, id)
		return err == nil && canManageSession(user, s.Meta.UserID)
	}
	refresh := func(ids ...string) {
		if refreshSession == nil {
			return
		}
		for _, id := range ids {
			_ = refreshSession(id)
		}
	}
	var out any
	switch str("action") {
	case "", "list":
		peers, err := sessionlink.Peers(layout, self)
		if err != nil {
			rsp.ToolError(w, req.ID, err.Error(), tool)
			return
		}
		out = map[string]any{"session": self, "linked": peers}
	case "read":
		if other == "" {
			rsp.ToolError(w, req.ID, "session is required", tool)
			return
		}
		if !sessionlink.IsLinked(layout, self, other) && !visible(other) {
			rsp.ToolError(w, req.ID, "session not found or not linked: "+other, tool)
			return
		}
		n := 6
		if v, ok := args["n"].(float64); ok && v > 0 {
			n = min(int(v), 20)
		}
		turns, err := sessionlink.Read(layout, other, n, 1500)
		if err != nil {
			rsp.ToolError(w, req.ID, err.Error(), tool)
			return
		}
		out = map[string]any{"session": other, "turns": turns}
	case "link":
		why := str("why")
		if other == "" || why == "" {
			rsp.ToolError(w, req.ID, "session and why are required", tool)
			return
		}
		if !visible(other) {
			rsp.ToolError(w, req.ID, "session not found: "+other, tool)
			return
		}
		if err := sessionlink.Link(layout, self, other, why); err != nil {
			rsp.ToolError(w, req.ID, err.Error(), tool)
			return
		}
		refresh(self, other)
		out = map[string]any{"ok": true, "linked": other, "why": why}
	case "unlink":
		if other == "" {
			rsp.ToolError(w, req.ID, "session is required", tool)
			return
		}
		if err := sessionlink.Unlink(layout, self, other); err != nil {
			rsp.ToolError(w, req.ID, err.Error(), tool)
			return
		}
		refresh(self, other)
		out = map[string]any{"ok": true, "unlinked": other}
	case "link_thread":
		chat, ts, why := str("chat_id"), str("thread_ts"), str("why")
		if chat == "" || ts == "" || why == "" {
			rsp.ToolError(w, req.ID, "chat_id, thread_ts and why are required", tool)
			return
		}
		if err := sessionlink.LinkThread(layout, self, str("channel"), chat, ts, why); err != nil {
			rsp.ToolError(w, req.ID, err.Error(), tool)
			return
		}
		out = map[string]any{"ok": true, "thread": fmt.Sprintf("%s/%s", chat, ts), "note": "the session a reply there starts will be linked to this one"}
	default:
		rsp.ToolError(w, req.ID, "action must be list, read, link, unlink or link_thread", tool)
		return
	}
	b, _ := json.Marshal(out)
	rsp.WriteResult(w, req.ID, ToolCallResult{Content: []ToolContent{{Type: "text", Text: string(b)}}})
}
