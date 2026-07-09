package agents

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/schedule"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/tools/agents/view"
	"github.com/yogasw/wick/pkg/tool"
)

// Scheduled messages (the Scheduled tab) — the USER-facing surface for a
// session's future message injections. Same store the wick_schedule_message
// MCP tool writes to; here the user lists, creates, and cancels schedules
// directly in the web UI. Access is owner-or-admin, mirroring ownsSession
// so a session the user can open is a session they can schedule into.

// scheduleVM is one schedule row in the Scheduled tab / global monitor.
type scheduleVM struct {
	ID           string `json:"id"`
	SessionID    string `json:"session_id"`
	SessionLabel string `json:"session_label,omitempty"` // filled by the global monitor for grouping
	CreatedBy    string `json:"created_by"`
	Kind       string `json:"kind"`
	RunAt      string `json:"run_at"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	RunCount   int    `json:"run_count"`
	Paused     bool   `json:"paused,omitempty"`
	IntervalMs int64  `json:"interval_ms,omitempty"`
	Cron       string `json:"cron,omitempty"`
	MaxRuns    int    `json:"max_runs,omitempty"`
	EndsAt     string `json:"ends_at,omitempty"`
	LastRunAt  string `json:"last_run_at,omitempty"`
	LastError  string `json:"last_error,omitempty"`
}

func scheduleToVM(m entity.ScheduledMessage) scheduleVM {
	vm := scheduleVM{
		ID:        m.ID,
		SessionID: m.SessionID,
		CreatedBy: m.CreatedBy,
		Kind:      m.Kind,
		RunAt:     m.RunAt.UTC().Format(time.RFC3339),
		Status:    m.Status,
		Message:   m.Message,
		RunCount:  m.RunCount,
		LastError: m.LastError,
	}
	if m.IsRecurring() {
		vm.Paused = m.Paused
		vm.IntervalMs = m.IntervalMs
		vm.Cron = m.Cron
		vm.MaxRuns = m.MaxRuns
		if m.EndsAt != nil {
			vm.EndsAt = m.EndsAt.UTC().Format(time.RFC3339)
		}
	}
	if m.LastRunAt != nil {
		vm.LastRunAt = m.LastRunAt.UTC().Format(time.RFC3339)
	}
	return vm
}

// scheduleResolveSession loads the target session and enforces access,
// writing the HTTP error itself and returning ok=false on any failure.
func scheduleResolveSession(c *tool.Ctx, sid string) (session.Session, bool) {
	if globalSchedule == nil {
		c.Error(http.StatusServiceUnavailable, "scheduling not ready")
		return session.Session{}, false
	}
	if strings.TrimSpace(sid) == "" {
		c.Error(http.StatusBadRequest, "session id required")
		return session.Session{}, false
	}
	sess, err := session.Load(globalLayout, sid)
	if err != nil {
		c.Error(http.StatusNotFound, "session not found")
		return session.Session{}, false
	}
	if !ownsSession(c, sess) {
		// Match the rest of the session surface: don't leak existence.
		c.Error(http.StatusNotFound, "session not found")
		return session.Session{}, false
	}
	return sess, true
}

// sessionSchedulesListUI returns this session's schedules (owner/admin only).
func sessionSchedulesListUI(c *tool.Ctx) {
	sid := c.PathValue("id")
	if _, ok := scheduleResolveSession(c, sid); !ok {
		return
	}
	// Scope to this session; owner filter is implied by the ownership check
	// above (a caller who owns/sees this session sees its schedules).
	rows, err := globalSchedule.ListForOwner(c.Context(), "", sid, true)
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]scheduleVM, 0, len(rows))
	for _, m := range rows {
		out = append(out, scheduleToVM(m))
	}
	c.JSON(http.StatusOK, map[string]any{"schedules": out})
}

// sessionSchedulesCreateUI schedules a new message into this session.
func sessionSchedulesCreateUI(c *tool.Ctx) {
	sid := c.PathValue("id")
	sess, ok := scheduleResolveSession(c, sid)
	if !ok {
		return
	}
	var body struct {
		RunAt     string `json:"run_at"`
		Every     string `json:"every"`
		Cron      string `json:"cron"`
		Message   string `json:"message"`
		AgentName string `json:"agent_name"`
		MaxRuns   int    `json:"max_runs"`
	}
	if err := c.BindJSON(&body); err != nil {
		c.Error(http.StatusBadRequest, "invalid JSON")
		return
	}
	message := strings.TrimSpace(body.Message)
	if message == "" {
		c.Error(http.StatusBadRequest, "message is required")
		return
	}
	if runes := []rune(message); len(runes) > scheduleMaxMessageRunes {
		c.Error(http.StatusBadRequest, fmt.Sprintf("message too long (max %d characters)", scheduleMaxMessageRunes))
		return
	}
	spec, err := schedule.ParseWhen(body.RunAt, body.Every, body.Cron, time.Now())
	if err != nil {
		c.Error(http.StatusBadRequest, err.Error())
		return
	}

	row := &entity.ScheduledMessage{
		SessionID:       sid,
		OwnerUserID:     sess.Meta.UserID,
		CreatedBy:       entity.ScheduledByUser,
		SourceSessionID: sid,
		AgentName:       strings.TrimSpace(body.AgentName),
		Message:         message,
		RunAt:           spec.FirstRunAt,
		MaxRuns:         body.MaxRuns,
	}
	if spec.Recurring {
		row.Kind = entity.ScheduledKindRecurring
		row.IntervalMs = spec.IntervalMs
		row.Cron = spec.Cron
	}
	m, err := globalSchedule.Create(c.Context(), row)
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, scheduleToVM(*m))
}

// sessionSchedulesMutateUI handles pause / resume / reschedule on one
// schedule of this session. action is fixed by the route wrapper.
func sessionSchedulesMutateUI(c *tool.Ctx, action string) {
	sid := c.PathValue("id")
	if _, ok := scheduleResolveSession(c, sid); !ok {
		return
	}
	scheduleID := c.PathValue("sid")
	m, err := globalSchedule.Get(c.Context(), scheduleID)
	if err != nil || m.SessionID != sid {
		c.Error(http.StatusNotFound, "schedule not found")
		return
	}

	switch action {
	case "pause":
		if !m.IsRecurring() {
			c.Error(http.StatusBadRequest, "only recurring schedules can be paused")
			return
		}
		err = globalSchedule.SetPaused(c.Context(), scheduleID, true, time.Time{})
	case "resume":
		if !m.IsRecurring() {
			c.Error(http.StatusBadRequest, "only recurring schedules can be resumed")
			return
		}
		var next time.Time
		if next, err = schedule.NextFrom(*m, time.Now()); err == nil {
			err = globalSchedule.SetPaused(c.Context(), scheduleID, false, next)
		}
	case "reschedule":
		var patch schedule.SchedulePatch
		if patch, err = scheduleParsePatchUI(*m, c, time.Now()); err == nil {
			err = globalSchedule.Reschedule(c.Context(), scheduleID, patch)
		}
	}
	if err != nil {
		if err == schedule.ErrNotFound {
			c.Error(http.StatusConflict, action+": schedule is not in a state that allows it")
			return
		}
		c.Error(http.StatusBadRequest, action+": "+err.Error())
		return
	}
	fresh, err := globalSchedule.Get(c.Context(), scheduleID)
	if err != nil {
		// Raced with a concurrent cancel/delete between the mutation and this
		// re-fetch — the action still landed, so report it gone rather than panic.
		c.Error(http.StatusNotFound, "schedule not found")
		return
	}
	c.JSON(http.StatusOK, scheduleToVM(*fresh))
}

// scheduleParsePatchUI builds a store patch from the reschedule request body.
func scheduleParsePatchUI(m entity.ScheduledMessage, c *tool.Ctx, now time.Time) (schedule.SchedulePatch, error) {
	var body struct {
		RunAt   string `json:"run_at"`
		Every   string `json:"every"`
		Cron    string `json:"cron"`
		Message string `json:"message"`
		MaxRuns *int   `json:"max_runs"`
	}
	if err := c.BindJSON(&body); err != nil {
		return schedule.SchedulePatch{}, fmt.Errorf("invalid JSON")
	}
	var patch schedule.SchedulePatch
	if body.RunAt != "" || body.Every != "" || body.Cron != "" {
		spec, err := schedule.ParseWhen(body.RunAt, body.Every, body.Cron, now)
		if err != nil {
			return patch, err
		}
		if spec.Recurring != m.IsRecurring() {
			return patch, fmt.Errorf("cannot change schedule kind; cancel and create a new one")
		}
		patch.RunAt = spec.FirstRunAt
		if spec.Recurring {
			iv := spec.IntervalMs
			cr := spec.Cron
			patch.IntervalMs = &iv
			patch.Cron = &cr
		}
	}
	if msg := strings.TrimSpace(body.Message); msg != "" {
		patch.Message = &msg
	}
	if body.MaxRuns != nil {
		patch.MaxRuns = body.MaxRuns
	}
	return patch, nil
}

// sessionSchedulesCancelUI cancels a pending schedule on this session.
func sessionSchedulesCancelUI(c *tool.Ctx) {
	sid := c.PathValue("id")
	if _, ok := scheduleResolveSession(c, sid); !ok {
		return
	}
	scheduleID := c.PathValue("sid")
	if strings.TrimSpace(scheduleID) == "" {
		c.Error(http.StatusBadRequest, "schedule id required")
		return
	}
	// Confirm the schedule belongs to this session before cancelling so a
	// caller can't cancel another session's schedule by guessing an id.
	m, err := globalSchedule.Get(c.Context(), scheduleID)
	if err != nil || m.SessionID != sid {
		c.Error(http.StatusNotFound, "schedule not found")
		return
	}
	if err := globalSchedule.Cancel(c.Context(), scheduleID); err != nil {
		if err == schedule.ErrNotFound {
			c.Error(http.StatusNotFound, "schedule not found or not pending")
			return
		}
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	c.JSON(http.StatusOK, map[string]any{"id": scheduleID, "status": entity.ScheduledStatusCancelled})
}

// scheduleMaxMessageRunes caps a scheduled message length (mirrors the MCP
// tool's limit).
const scheduleMaxMessageRunes = 8000

// ── Global cross-session monitor ─────────────────────────────────────
//
// The Scheduled page lists schedules across every session the caller may
// see. Access reuses the exact session-visibility filter the sidebar uses
// (callerProjectAccess.allowSession): a user sees schedules for sessions they
// own or reach via a project; an admin sees all ONLY when admin_see_all is on
// — otherwise an admin is scoped like a regular user. Provenance (creator,
// session) rides along so the page can group + attribute each row.

// scheduledPage renders the global scheduler-monitor SPA shell inside the
// agents chrome. Data + access are served by schedulesAllUI.
func scheduledPage(c *tool.Ctx) {
	c.HTML(view.ScheduledSPA(view.ScheduledSPAVM{
		Layout:   sidebarVM(c, "scheduled", ""),
		Base:     c.Base(),
		AssetURL: spaAssetURL("scheduled"),
	}))
}

// schedulesAllUI lists schedules for every session the caller may access,
// tagged with the session label for grouping.
func schedulesAllUI(c *tool.Ctx) {
	if globalSchedule == nil || globalMgr == nil {
		c.JSON(http.StatusOK, map[string]any{"schedules": []any{}})
		return
	}
	rows, err := globalSchedule.ListAll(c.Context(), 2000)
	if err != nil {
		c.Error(http.StatusInternalServerError, err.Error())
		return
	}
	access := callerProjectAccess(c)
	sessions := globalMgr.Registry().Sessions()
	out := make([]scheduleVM, 0, len(rows))
	for _, m := range rows {
		sess, ok := sessions[m.SessionID]
		if !ok {
			// Session no longer in the registry — hide from the monitor rather
			// than leak an orphan the caller can't verify access to.
			continue
		}
		if !access.allowSession(sess.Meta.ProjectID, sess.Meta.UserID) {
			continue
		}
		vm := scheduleToVM(m)
		vm.SessionLabel = firstNonEmptyStr(sess.Meta.Label, m.SessionID)
		out = append(out, vm)
	}
	c.JSON(http.StatusOK, map[string]any{"schedules": out})
}

// scheduleByIDMutateUI runs cancel/pause/resume on a schedule addressed by id
// alone (the global page has no session in the path). Access is checked via
// the schedule's own session, using the same allowSession filter.
func scheduleByIDMutateUI(c *tool.Ctx, action string) {
	if globalSchedule == nil || globalMgr == nil {
		c.Error(http.StatusServiceUnavailable, "scheduling not ready")
		return
	}
	scheduleID := c.PathValue("sid")
	if strings.TrimSpace(scheduleID) == "" {
		c.Error(http.StatusBadRequest, "schedule id required")
		return
	}
	m, err := globalSchedule.Get(c.Context(), scheduleID)
	if err != nil {
		c.Error(http.StatusNotFound, "schedule not found")
		return
	}
	// Gate on the schedule's session, same visibility rule as the monitor.
	sess, ok := globalMgr.Registry().Sessions()[m.SessionID]
	if !ok || !callerProjectAccess(c).allowSession(sess.Meta.ProjectID, sess.Meta.UserID) {
		c.Error(http.StatusNotFound, "schedule not found")
		return
	}

	switch action {
	case "cancel":
		err = globalSchedule.Cancel(c.Context(), scheduleID)
	case "pause":
		if !m.IsRecurring() {
			c.Error(http.StatusBadRequest, "only recurring schedules can be paused")
			return
		}
		err = globalSchedule.SetPaused(c.Context(), scheduleID, true, time.Time{})
	case "resume":
		if !m.IsRecurring() {
			c.Error(http.StatusBadRequest, "only recurring schedules can be resumed")
			return
		}
		var next time.Time
		if next, err = schedule.NextFrom(*m, time.Now()); err == nil {
			err = globalSchedule.SetPaused(c.Context(), scheduleID, false, next)
		}
	}
	if err != nil {
		if err == schedule.ErrNotFound {
			c.Error(http.StatusConflict, action+": schedule is not in a state that allows it")
			return
		}
		c.Error(http.StatusBadRequest, action+": "+err.Error())
		return
	}
	fresh, err := globalSchedule.Get(c.Context(), scheduleID)
	if err != nil {
		c.Error(http.StatusNotFound, "schedule not found")
		return
	}
	c.JSON(http.StatusOK, scheduleToVM(*fresh))
}

func firstNonEmptyStr(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
