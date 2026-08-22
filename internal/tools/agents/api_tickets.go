package agents

import (
	"net/http"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

/* ── DTOs ────────────────────────────────────────────────────────────────── */

// TicketItem is one card on the project ticket board. Sessions whose
// Ticket is still nil (created moments ago, sweeper hasn't adopted them
// yet) are surfaced as virtual "open" tickets so the board never hides a
// chat.
type TicketItem struct {
	SessionID  string            `json:"session_id"`
	Title      string            `json:"title"`
	Status     string            `json:"status"`
	Assignee   string            `json:"assignee,omitempty"`
	Fields     map[string]string `json:"fields,omitempty"`
	UpdatedAt  string            `json:"updated_at,omitempty"`
	LastActive string            `json:"last_active"`
	Stale      bool              `json:"stale"`
	OwnerID    string            `json:"owner_id,omitempty"`
	Lifecycle  string            `json:"lifecycle,omitempty"`
}

// ticketBoardResponse is the envelope for GET /api/projects/{id}/tickets.
type ticketBoardResponse struct {
	Config  project.TicketConfig `json:"config"`
	Tickets []TicketItem         `json:"tickets"`
	// Users maps every user ID appearing in Tickets (owners+assignees)
	// to a display name, so the board renders names without a second
	// round-trip. Best effort — unknown IDs are simply absent.
	Users map[string]string `json:"users,omitempty"`
	// Me is the caller's user ID, for the "Assignee: Me" filter and the
	// "Assign to me" button.
	Me string `json:"me,omitempty"`
	// Statuses is the fixed board column order.
	Statuses []string `json:"statuses"`
}

/* ── handlers ────────────────────────────────────────────────────────────── */

// apiProjectTickets handles GET /api/projects/{id}/tickets — the ticket
// board payload. Access enforced by projectAccessMW.
func apiProjectTickets(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	p, ok := globalMgr.Registry().Project(id)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	cfg := p.Meta.Ticket

	lifecycle := map[string]string{}
	if globalPool != nil {
		for _, e := range globalPool.ActiveSnapshot() {
			lifecycle[e.SessionID] = e.Lifecycle
		}
	}

	now := time.Now()
	items := []TicketItem{}
	userIDs := map[string]bool{}
	for sid, s := range globalMgr.Registry().Sessions() {
		if s.Meta.ProjectID != id || s.Meta.ParentSessionID != "" {
			continue
		}
		item := TicketItem{
			SessionID:  sid,
			Title:      loadFirstUserMessage(globalLayout, sid, 60),
			Status:     session.TicketOpen,
			LastActive: s.Meta.LastActive.Format(time.RFC3339),
			OwnerID:    s.Meta.UserID,
			Lifecycle:  lifecycle[sid],
		}
		if t := s.Meta.Ticket; t != nil {
			item.Status = t.Status
			item.Assignee = t.Assignee
			item.Fields = t.Fields
			if !t.UpdatedAt.IsZero() {
				item.UpdatedAt = t.UpdatedAt.Format(time.RFC3339)
			}
			item.Stale = ticket.NeedsFollowup(cfg, t, now)
		}
		if item.OwnerID != "" {
			userIDs[item.OwnerID] = true
		}
		if item.Assignee != "" {
			userIDs[item.Assignee] = true
		}
		items = append(items, item)
	}

	resp := ticketBoardResponse{
		Config:   cfg,
		Tickets:  items,
		Statuses: session.TicketStatuses,
		Users:    map[string]string{},
	}
	if u := login.GetUser(c.Context()); u != nil {
		resp.Me = u.ID
		userIDs[u.ID] = true
	}
	if globalAuth != nil {
		for uid := range userIDs {
			if usr, err := globalAuth.GetUserByID(c.Context(), uid); err == nil && usr != nil {
				resp.Users[uid] = usr.Name
			}
		}
	}
	c.JSON(http.StatusOK, resp)
}

// apiProjectTicketConfig handles PUT /api/projects/{id}/ticket-config.
// Saves the project's ticket-mode configuration. Access enforced by
// projectAccessMW; field schema is validated here so a broken schema is
// refused rather than stored.
func apiProjectTicketConfig(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	var req project.TicketConfig
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	seen := map[string]bool{}
	for i, f := range req.Fields {
		key := strings.TrimSpace(f.Key)
		if key == "" {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "field key is required"})
			return
		}
		if seen[key] {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "duplicate field key: " + key})
			return
		}
		seen[key] = true
		if f.Type != "text" && f.Type != "select" {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "field " + key + ": type must be text or select"})
			return
		}
		if f.Type == "select" && len(f.Options) == 0 {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "field " + key + ": select needs at least one option"})
			return
		}
		req.Fields[i].Key = key
		if strings.TrimSpace(f.Label) == "" {
			req.Fields[i].Label = key
		}
	}
	if req.FollowupAfterSec < 0 || req.AutoResolveAfterSec < 0 {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "durations must be >= 0"})
		return
	}
	// First enable with an empty schema gets the seed fields, so the
	// board is useful before anyone visits the field editor.
	if req.Enabled && len(req.Fields) == 0 {
		req.Fields = project.DefaultTicketFields()
	}

	p, ok := globalMgr.Registry().Project(id)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "project not found"})
		return
	}
	meta := p.Meta
	meta.Ticket = req
	if _, err := globalMgr.UpdateProject(c.Context(), id, meta); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// apiSessionTicketUpdate handles PUT /api/sessions/{id}/ticket — partial
// update of one session's ticket (status, assignee, fields). Access
// enforced by sessionAccessMW. Any edit bumps UpdatedAt, which is what
// the stale/auto-resolve timers key on.
func apiSessionTicketUpdate(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	var req struct {
		Status   *string           `json:"status"`
		Assignee *string           `json:"assignee"`
		Fields   map[string]string `json:"fields"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if req.Status != nil && !session.ValidTicketStatus(*req.Status) {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid status: " + *req.Status})
		return
	}

	sess, err := session.Load(globalLayout, id)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	t := sess.Meta.Ticket
	if t == nil {
		t = &session.Ticket{Status: session.TicketOpen}
	}
	if req.Status != nil {
		t.Status = *req.Status
	}
	if req.Assignee != nil {
		t.Assignee = strings.TrimSpace(*req.Assignee)
	}
	if req.Fields != nil {
		if t.Fields == nil {
			t.Fields = map[string]string{}
		}
		for k, v := range req.Fields {
			if strings.TrimSpace(v) == "" {
				delete(t.Fields, k)
				continue
			}
			t.Fields[k] = v
		}
	}
	t.UpdatedAt = time.Now().UTC()
	sess.Meta.Ticket = t
	if err := session.SaveMeta(globalLayout, id, sess.Meta); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	_ = globalMgr.RefreshSession(id)
	c.JSON(http.StatusOK, map[string]any{"status": "ok", "ticket": t})
}

// sessionTicketResponse is the envelope for GET /api/sessions/{id}/ticket —
// everything the conversation rail's Ticket panel needs in one call.
type sessionTicketResponse struct {
	Config   project.TicketConfig `json:"config"`
	Ticket   *session.Ticket      `json:"ticket"`
	Statuses []string             `json:"statuses"`
	Users    map[string]string    `json:"users,omitempty"`
	Me       string               `json:"me,omitempty"`
}

// apiSessionTicketGet handles GET /api/sessions/{id}/ticket. Access
// enforced by sessionAccessMW. A session outside any ticket-enabled
// project answers with config.enabled=false — the panel hides itself.
func apiSessionTicketGet(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	sess, ok := globalMgr.Registry().Session(id)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	resp := sessionTicketResponse{
		Ticket:   sess.Meta.Ticket,
		Statuses: session.TicketStatuses,
		Users:    map[string]string{},
	}
	if p, pok := globalMgr.Registry().Project(sess.Meta.ProjectID); pok {
		resp.Config = p.Meta.Ticket
	}
	ids := map[string]bool{}
	if sess.Meta.UserID != "" {
		ids[sess.Meta.UserID] = true
	}
	if sess.Meta.Ticket != nil && sess.Meta.Ticket.Assignee != "" {
		ids[sess.Meta.Ticket.Assignee] = true
	}
	if u := login.GetUser(c.Context()); u != nil {
		resp.Me = u.ID
		ids[u.ID] = true
	}
	if globalAuth != nil {
		for uid := range ids {
			if usr, err := globalAuth.GetUserByID(c.Context(), uid); err == nil && usr != nil {
				resp.Users[uid] = usr.Name
			}
		}
	}
	c.JSON(http.StatusOK, resp)
}

// apiTicketFilterGet handles GET /api/me/ticket-filters/{projectID} —
// the caller's saved board filter for one project.
func apiTicketFilterGet(c *tool.Ctx) {
	u := login.GetUser(c.Context())
	if u == nil {
		c.JSON(http.StatusOK, entity.TicketFilter{})
		return
	}
	c.JSON(http.StatusOK, u.Metadata.TicketFilters[c.PathValue("projectID")])
}

// apiTicketFilterSave handles PUT /api/me/ticket-filters/{projectID}.
func apiTicketFilterSave(c *tool.Ctx) {
	u := login.GetUser(c.Context())
	if u == nil {
		c.JSON(http.StatusUnauthorized, map[string]string{"error": "login required"})
		return
	}
	var f entity.TicketFilter
	if err := c.BindJSON(&f); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	if globalAuth == nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": "auth service unavailable"})
		return
	}
	if err := globalAuth.SetTicketFilter(c.Context(), u.ID, c.PathValue("projectID"), f); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}
