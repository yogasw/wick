package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/login"
)

// titleMaxRunes caps a session title so the sidebar never overflows.
// Matches the truncation applied to the auto-derived first-message label.
const titleMaxRunes = 60

// WickSessionInfo handles the wick_session_info tool — a read-only view
// of one session's meta so the agent can decide whether to set a title.
// Returns title (current Label), title_custom (true = already explicitly
// set by a human or the agent), origin, status, and project_id.
func WickSessionInfo(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, layout agentconfig.Layout, args map[string]any) {
	sessionID, _ := args["session_id"].(string)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		rsp.ToolError(w, req.ID, "session_id is required", "wick_session_info")
		return
	}
	sess, err := session.Load(layout, sessionID)
	if err != nil {
		rsp.ToolError(w, req.ID, "load session: "+err.Error(), "wick_session_info")
		return
	}
	caller := login.GetUser(r.Context())
	if caller != nil && !caller.CanSeeAllSessions() && sess.Meta.UserID != "" && sess.Meta.UserID != caller.ID {
		rsp.ToolError(w, req.ID, fmt.Sprintf("session not found: %s", sessionID), "wick_session_info")
		return
	}
	out := map[string]any{
		"session_id":   sess.ID,
		"title":        sess.Meta.Label,
		"title_custom": sess.Meta.TitleCustom,
		"origin":       string(sess.Meta.Origin),
		"status":       string(sess.Meta.Status),
		"project_id":   sess.Meta.ProjectID,
	}
	b, _ := json.Marshal(out)
	rsp.WriteResult(w, req.ID, ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: string(b)}},
	})
}

// WickSetTitle handles the wick_set_title tool. It writes an explicit
// session title into meta.Label and marks TitleCustom=true so the
// auto-derived first-message label never overwrites it. Always replaces
// whatever title is currently set — the caller is expected to read
// wick_session_info first when it only wants to fill an unset title.
//
// refreshSession, when non-nil, reloads the session into the in-memory
// registry so the live dashboard reflects the new title immediately.
func WickSetTitle(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, layout agentconfig.Layout, refreshSession func(id string) error, args map[string]any) {
	sessionID, _ := args["session_id"].(string)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		rsp.ToolError(w, req.ID, "session_id is required", "wick_set_title")
		return
	}
	title, _ := args["title"].(string)
	title = strings.TrimSpace(title)
	if title == "" {
		rsp.ToolError(w, req.ID, "title is required", "wick_set_title")
		return
	}
	if runes := []rune(title); len(runes) > titleMaxRunes {
		title = string(runes[:titleMaxRunes])
	}

	sess, err := session.Load(layout, sessionID)
	if err != nil {
		rsp.ToolError(w, req.ID, "load session: "+err.Error(), "wick_set_title")
		return
	}
	caller := login.GetUser(r.Context())
	if caller != nil && !caller.CanSeeAllSessions() && sess.Meta.UserID != "" && sess.Meta.UserID != caller.ID {
		rsp.ToolError(w, req.ID, fmt.Sprintf("session not found: %s", sessionID), "wick_set_title")
		return
	}
	sess.Meta.Label = title
	sess.Meta.TitleCustom = true
	if err := session.SaveMeta(layout, sessionID, sess.Meta); err != nil {
		rsp.ToolError(w, req.ID, "save title: "+err.Error(), "wick_set_title")
		return
	}
	if refreshSession != nil {
		_ = refreshSession(sessionID)
	}

	b, _ := json.Marshal(map[string]any{
		"session_id":   sess.ID,
		"title":        title,
		"title_custom": true,
	})
	rsp.WriteResult(w, req.ID, ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: string(b)}},
	})
}
