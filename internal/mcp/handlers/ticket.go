package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/login"
)

// ticketSnapshot renders a ticket + its project schema as the tool
// result payload shared by wick_ticket_get and wick_ticket_set.
func ticketSnapshot(sess session.Session, cfg project.TicketConfig) map[string]any {
	out := map[string]any{
		"session_id":     sess.ID,
		"title":          sess.Meta.Label,
		"ticket_enabled": cfg.Enabled,
		"statuses":       session.TicketStatuses,
		"schema":         cfg.Fields,
	}
	if t := sess.Meta.Ticket; t != nil {
		out["status"] = t.Status
		out["assignee"] = t.Assignee
		out["fields"] = t.Fields
		out["updated_at"] = t.UpdatedAt.UTC().Format(time.RFC3339)
	} else {
		out["status"] = session.TicketOpen
	}
	return out
}

// loadTicketProject returns the ticket config of the session's project.
// A session without a project (or an unreadable project) reads as
// config-off, which the tools surface as ticket_enabled=false.
func loadTicketProject(layout agentconfig.Layout, sess session.Session) project.TicketConfig {
	if sess.Meta.ProjectID == "" {
		return project.TicketConfig{}
	}
	p, err := project.Load(layout, sess.Meta.ProjectID)
	if err != nil {
		return project.TicketConfig{}
	}
	return p.Meta.Ticket
}

// WickTicketGet handles the wick_ticket_get tool — the read side: the
// session's current ticket plus the project's field schema so the agent
// knows which keys wick_ticket_set accepts.
func WickTicketGet(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, layout agentconfig.Layout, args map[string]any) {
	sessionID, _ := args["session_id"].(string)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		rsp.ToolError(w, req.ID, "session_id is required", "wick_ticket_get")
		return
	}
	sess, err := session.Load(layout, sessionID)
	if err != nil {
		rsp.ToolError(w, req.ID, "load session: "+err.Error(), "wick_ticket_get")
		return
	}
	if !canManageSession(login.GetUser(r.Context()), sess.Meta.UserID) {
		rsp.ToolError(w, req.ID, fmt.Sprintf("session not found: %s", sessionID), "wick_ticket_get")
		return
	}
	b, _ := json.Marshal(ticketSnapshot(sess, loadTicketProject(layout, sess)))
	rsp.WriteResult(w, req.ID, ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: string(b)}},
	})
}

// WickTicketSet handles the wick_ticket_set tool. Partial update: only
// the provided keys change. Any successful update bumps UpdatedAt, which
// resets the stale/auto-resolve timers — so an agent acting on a followup
// should always end by calling this.
func WickTicketSet(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, layout agentconfig.Layout, refreshSession func(id string) error, args map[string]any) {
	sessionID, _ := args["session_id"].(string)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		rsp.ToolError(w, req.ID, "session_id is required", "wick_ticket_set")
		return
	}

	sess, err := session.Load(layout, sessionID)
	if err != nil {
		rsp.ToolError(w, req.ID, "load session: "+err.Error(), "wick_ticket_set")
		return
	}
	if !canManageSession(login.GetUser(r.Context()), sess.Meta.UserID) {
		rsp.ToolError(w, req.ID, fmt.Sprintf("session not found: %s", sessionID), "wick_ticket_set")
		return
	}

	t := sess.Meta.Ticket
	if t == nil {
		t = &session.Ticket{Status: session.TicketOpen}
	}
	changed := false
	if v, ok := args["status"].(string); ok && v != "" {
		if !session.ValidTicketStatus(v) {
			rsp.ToolError(w, req.ID, fmt.Sprintf("invalid status %q (want one of %s)", v, strings.Join(session.TicketStatuses, ", ")), "wick_ticket_set")
			return
		}
		t.Status = v
		changed = true
	}
	if v, ok := args["assignee"].(string); ok {
		t.Assignee = strings.TrimSpace(v)
		changed = true
	}
	if raw, ok := args["fields"].(map[string]any); ok && len(raw) > 0 {
		if t.Fields == nil {
			t.Fields = map[string]string{}
		}
		for k, v := range raw {
			s, isStr := v.(string)
			if !isStr {
				rsp.ToolError(w, req.ID, fmt.Sprintf("field %q must be a string", k), "wick_ticket_set")
				return
			}
			if strings.TrimSpace(s) == "" {
				delete(t.Fields, k)
			} else {
				t.Fields[k] = s
			}
		}
		changed = true
	}
	if !changed {
		rsp.ToolError(w, req.ID, "nothing to update: pass status, assignee, and/or fields", "wick_ticket_set")
		return
	}

	t.UpdatedAt = time.Now().UTC()
	sess.Meta.Ticket = t
	if err := session.SaveMeta(layout, sessionID, sess.Meta); err != nil {
		rsp.ToolError(w, req.ID, "save ticket: "+err.Error(), "wick_ticket_set")
		return
	}
	if refreshSession != nil {
		_ = refreshSession(sessionID)
	}

	b, _ := json.Marshal(ticketSnapshot(sess, loadTicketProject(layout, sess)))
	rsp.WriteResult(w, req.ID, ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: string(b)}},
	})
}
