package tickets

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/notes"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/ticket"
	"github.com/yogasw/wick/pkg/connector"
)

// resolveProject returns the project to work in: the explicit input when
// given, otherwise the calling session's. Ops accept an implicit project so
// an agent working inside a session never has to be told where it is.
func resolveProject(layout agentconfig.Layout, c *connector.Ctx, explicit string) (string, error) {
	if p := strings.TrimSpace(explicit); p != "" {
		return p, nil
	}
	sid := c.SessionID()
	if sid == "" {
		return "", fmt.Errorf("project_id is required (no calling session to infer it from)")
	}
	sess, err := session.Load(layout, sid)
	if err != nil {
		return "", fmt.Errorf("load calling session: %w", err)
	}
	if sess.Meta.ProjectID == "" {
		return "", fmt.Errorf("this session belongs to no project, so project_id must be given")
	}
	return sess.Meta.ProjectID, nil
}

// resolveTicket returns the ticket to act on: the explicit id when given,
// otherwise the one the calling session is attached to.
func resolveTicket(layout agentconfig.Layout, c *connector.Ctx, projectID, explicit string) (ticket.Ticket, error) {
	if id := strings.TrimSpace(explicit); id != "" {
		tk, err := ticket.Load(layout, projectID, id)
		if err != nil {
			return ticket.Ticket{}, fmt.Errorf("ticket %q not found in project %s", id, projectID)
		}
		return tk, nil
	}
	sid := c.SessionID()
	if sid == "" {
		return ticket.Ticket{}, fmt.Errorf("ticket_id is required (no calling session to infer it from)")
	}
	tk, ok := ticket.FindBySession(layout, projectID, sid)
	if !ok {
		return ticket.Ticket{}, fmt.Errorf("this session is not attached to a ticket — pass ticket_id, or attach it first")
	}
	return tk, nil
}

// resolveSession returns the session id to attach/detach.
func resolveSession(c *connector.Ctx, explicit string) (string, error) {
	if s := strings.TrimSpace(explicit); s != "" {
		return s, nil
	}
	if sid := c.SessionID(); sid != "" {
		return sid, nil
	}
	return "", fmt.Errorf("session_id is required (no calling session to infer it from)")
}

// parseFields decodes the JSON field map ops accept as a string.
func parseFields(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("fields must be a JSON object of string values: %w", err)
	}
	return out, nil
}

// ticketView is the shape ops return. Sessions and note counts are included
// because "how much work is on this" is the first thing worth knowing, and
// fetching it separately would cost another round-trip.
type ticketView struct {
	ID        string            `json:"id"`
	ProjectID string            `json:"project_id"`
	Title     string            `json:"title"`
	Status    string            `json:"status"`
	Assignee  string            `json:"assignee,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
	Sessions  []string          `json:"sessions,omitempty"`
	Notes     int               `json:"notes"`
	OpenTasks int               `json:"open_tasks"`
	UpdatedAt string            `json:"updated_at"`
}

func (h *handlers) view(tk ticket.Ticket) ticketView {
	count, _ := notes.Counts(h.layout, notes.Scope{ProjectID: tk.ProjectID, TicketID: tk.ID})
	return ticketView{
		ID: tk.ID, ProjectID: tk.ProjectID, Title: tk.Title, Status: tk.Status,
		Assignee: tk.Assignee, Fields: tk.Fields, Sessions: tk.Sessions,
		Notes: count.Visible, OpenTasks: count.OpenTasks,
		UpdatedAt: tk.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

func (h *handlers) list(c *connector.Ctx) (any, error) {
	projectID, err := resolveProject(h.layout, c, c.Input("project_id"))
	if err != nil {
		return nil, err
	}
	all, err := ticket.List(h.layout, projectID)
	if err != nil {
		return nil, fmt.Errorf("list tickets: %w", err)
	}
	filter := strings.TrimSpace(c.Input("status"))
	if filter != "" && !ticket.ValidStatus(filter) {
		return nil, fmt.Errorf("invalid status %q (want %s)", filter, strings.Join(ticket.Statuses, ", "))
	}
	out := make([]ticketView, 0, len(all))
	for _, tk := range all {
		if filter != "" && tk.Status != filter {
			continue
		}
		out = append(out, h.view(tk))
	}
	return map[string]any{"project_id": projectID, "tickets": out, "total": len(out)}, nil
}

func (h *handlers) get(c *connector.Ctx) (any, error) {
	projectID, err := resolveProject(h.layout, c, c.Input("project_id"))
	if err != nil {
		return nil, err
	}
	tk, err := resolveTicket(h.layout, c, projectID, c.Input("ticket_id"))
	if err != nil {
		return nil, err
	}
	return h.view(tk), nil
}

func (h *handlers) create(c *connector.Ctx) (any, error) {
	projectID, err := resolveProject(h.layout, c, c.Input("project_id"))
	if err != nil {
		return nil, err
	}
	fields, err := parseFields(c.Input("fields"))
	if err != nil {
		return nil, err
	}
	var seed []string
	if c.InputBool("attach_current_session") {
		sid := c.SessionID()
		if sid == "" {
			return nil, fmt.Errorf("attach_current_session was set but there is no calling session")
		}
		seed = []string{sid}
	}
	// An agent creating a ticket does so on somebody's behalf, so the ticket
	// lands with that person rather than unassigned. The agent can still
	// pass an explicit assignee (including nobody, via a space).
	assignee := strings.TrimSpace(c.Input("assignee"))
	if _, given := c.RawInputValue("assignee"); !given {
		assignee = c.CallerUserID()
	}
	tk, err := ticket.Create(h.layout, ticket.CreateOptions{
		ProjectID: projectID,
		Title:     c.Input("title"),
		Status:    strings.TrimSpace(c.Input("status")),
		Assignee:  assignee,
		Fields:    fields,
		Sessions:  seed,
	})
	if err != nil {
		return nil, err
	}
	for _, sid := range seed {
		writeBackPointer(h.layout, sid, tk.ID)
	}
	return h.view(tk), nil
}

func (h *handlers) update(c *connector.Ctx) (any, error) {
	projectID, err := resolveProject(h.layout, c, c.Input("project_id"))
	if err != nil {
		return nil, err
	}
	tk, err := resolveTicket(h.layout, c, projectID, c.Input("ticket_id"))
	if err != nil {
		return nil, err
	}
	if s := strings.TrimSpace(c.Input("status")); s != "" {
		if !ticket.ValidStatus(s) {
			return nil, fmt.Errorf("invalid status %q (want %s)", s, strings.Join(ticket.Statuses, ", "))
		}
		tk.Status = s
	}
	if t := strings.TrimSpace(c.Input("title")); t != "" {
		tk.Title = t
	}
	// Assignee is cleared by an explicitly-present empty value, which is
	// how "unassign" is expressed without a separate op.
	if raw, ok := c.RawInputValue("assignee"); ok {
		if s, isStr := raw.(string); isStr {
			tk.Assignee = strings.TrimSpace(s)
		}
	}
	fields, err := parseFields(c.Input("fields"))
	if err != nil {
		return nil, err
	}
	if fields != nil {
		if tk.Fields == nil {
			tk.Fields = map[string]string{}
		}
		for k, v := range fields {
			if strings.TrimSpace(v) == "" {
				delete(tk.Fields, k)
				continue
			}
			tk.Fields[k] = v
		}
	}
	if err := ticket.Save(h.layout, tk); err != nil {
		return nil, err
	}
	return h.view(tk), nil
}

func (h *handlers) attach(c *connector.Ctx) (any, error) {
	projectID, err := resolveProject(h.layout, c, c.Input("project_id"))
	if err != nil {
		return nil, err
	}
	ticketID := strings.TrimSpace(c.Input("ticket_id"))
	if ticketID == "" {
		return nil, fmt.Errorf("ticket_id is required")
	}
	sid, err := resolveSession(c, c.Input("session_id"))
	if err != nil {
		return nil, err
	}
	if err := ticket.AttachSession(h.layout, projectID, ticketID, sid); err != nil {
		return nil, err
	}
	writeBackPointer(h.layout, sid, ticketID)
	tk, err := ticket.Load(h.layout, projectID, ticketID)
	if err != nil {
		return nil, err
	}
	return h.view(tk), nil
}

func (h *handlers) detach(c *connector.Ctx) (any, error) {
	projectID, err := resolveProject(h.layout, c, c.Input("project_id"))
	if err != nil {
		return nil, err
	}
	ticketID := strings.TrimSpace(c.Input("ticket_id"))
	if ticketID == "" {
		return nil, fmt.Errorf("ticket_id is required")
	}
	sid, err := resolveSession(c, c.Input("session_id"))
	if err != nil {
		return nil, err
	}
	if err := ticket.DetachSession(h.layout, projectID, ticketID, sid); err != nil {
		return nil, err
	}
	writeBackPointer(h.layout, sid, "")
	tk, err := ticket.Load(h.layout, projectID, ticketID)
	if err != nil {
		return nil, err
	}
	return h.view(tk), nil
}

// writeBackPointer keeps session.Meta.TicketID in step with the ticket's
// own list. Best effort: the ticket is the record, so a failure here
// degrades a lookup shortcut, never correctness.
func writeBackPointer(layout agentconfig.Layout, sessionID, ticketID string) {
	sess, err := session.Load(layout, sessionID)
	if err != nil || sess.Meta.TicketID == ticketID {
		return
	}
	sess.Meta.TicketID = ticketID
	_ = session.SaveMeta(layout, sessionID, sess.Meta)
}

// settingsView is the shape the settings ops return. Durations come back in
// the same units the setter takes, so a round-trip does not silently change
// them.
type settingsView struct {
	ProjectID            string                   `json:"project_id"`
	Enabled              bool                     `json:"enabled"`
	Fields               []project.TicketField    `json:"fields,omitempty"`
	FollowupAfterMinutes int64                    `json:"followup_after_minutes"`
	AutoResolveAfterDays float64                  `json:"auto_resolve_after_days"`
	FollowupPrompt       string                   `json:"followup_prompt,omitempty"`
	AutoCreate           []project.AutoCreateRule `json:"auto_create,omitempty"`
	Statuses             []string                 `json:"statuses"`
}

func viewSettings(projectID string, cfg project.TicketConfig) settingsView {
	return settingsView{
		ProjectID:            projectID,
		Enabled:              cfg.Enabled,
		Fields:               cfg.Fields,
		FollowupAfterMinutes: cfg.FollowupAfterSec / 60,
		AutoResolveAfterDays: float64(cfg.AutoResolveAfterSec) / 86400,
		FollowupPrompt:       cfg.FollowupPrompt,
		AutoCreate:           cfg.AutoCreate,
		Statuses:             ticket.Statuses,
	}
}

func (h *handlers) settingsGet(c *connector.Ctx) (any, error) {
	projectID, err := resolveProject(h.layout, c, c.Input("project_id"))
	if err != nil {
		return nil, err
	}
	p, err := project.Load(h.layout, projectID)
	if err != nil {
		return nil, fmt.Errorf("load project %q: %w", projectID, err)
	}
	return viewSettings(projectID, p.Meta.Ticket), nil
}

func (h *handlers) settingsSet(c *connector.Ctx) (any, error) {
	projectID, err := resolveProject(h.layout, c, c.Input("project_id"))
	if err != nil {
		return nil, err
	}
	p, err := project.Load(h.layout, projectID)
	if err != nil {
		return nil, fmt.Errorf("load project %q: %w", projectID, err)
	}
	cfg := p.Meta.Ticket
	touched := false

	switch strings.TrimSpace(strings.ToLower(c.Input("enabled"))) {
	case "true":
		cfg.Enabled, touched = true, true
	case "false":
		cfg.Enabled, touched = false, true
	case "":
	default:
		return nil, fmt.Errorf("enabled must be true or false")
	}

	// Minutes and days in, seconds stored: an agent that writes "60" for an
	// hour is likelier to be right than one converting to 3600 itself.
	if v := strings.TrimSpace(c.Input("followup_after_minutes")); v != "" {
		mins, perr := strconv.ParseFloat(v, 64)
		if perr != nil || mins < 0 {
			return nil, fmt.Errorf("followup_after_minutes must be a number >= 0")
		}
		cfg.FollowupAfterSec = int64(mins * 60)
		touched = true
	}
	if v := strings.TrimSpace(c.Input("auto_resolve_after_days")); v != "" {
		days, perr := strconv.ParseFloat(v, 64)
		if perr != nil || days < 0 {
			return nil, fmt.Errorf("auto_resolve_after_days must be a number >= 0")
		}
		cfg.AutoResolveAfterSec = int64(days * 86400)
		touched = true
	}
	if raw, ok := c.RawInputValue("followup_prompt"); ok {
		if s, isStr := raw.(string); isStr {
			cfg.FollowupPrompt = s
			touched = true
		}
	}
	if v := strings.TrimSpace(c.Input("auto_create")); v != "" {
		var rules []project.AutoCreateRule
		if jerr := json.Unmarshal([]byte(v), &rules); jerr != nil {
			return nil, fmt.Errorf("auto_create must be a JSON array of rules: %w", jerr)
		}
		// Validated before storing, so a rule that could never fire (bad
		// regex, unknown channel kind) is reported instead of sitting in
		// the config looking active.
		if verr := ticket.ValidateAutoCreate(rules); verr != nil {
			return nil, verr
		}
		cfg.AutoCreate = rules
		touched = true
	}

	if !touched {
		return nil, fmt.Errorf("nothing to change: pass enabled, followup_after_minutes, auto_resolve_after_days, followup_prompt, and/or auto_create")
	}
	// Enabling with no schema yet gets the seed fields, matching what the
	// settings UI does — otherwise the board would have no fields at all.
	if cfg.Enabled && len(cfg.Fields) == 0 {
		cfg.Fields = project.DefaultTicketFields()
	}

	p.Meta.Ticket = cfg
	if serr := project.SaveMeta(h.layout, projectID, p.Meta); serr != nil {
		return nil, serr
	}
	return viewSettings(projectID, cfg), nil
}
