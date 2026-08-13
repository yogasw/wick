package agents

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/project"
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
	Kind         string `json:"kind"`
	// RunAt / NextRunAt carry the NEXT fire time and are empty on a
	// terminal row (there is no next fire). Both are sent: RunAt for the
	// SPA components that read it, NextRunAt as the honest name.
	//
	// Unlike the MCP view — which drops run_at on recurring rows, where the
	// name misleads an agent into reading it as a creation time — this is a
	// private contract with our own SPAs, and they use run_at for the "next"
	// line. Keeping both here costs nothing and avoids a churn-only rename.
	RunAt     string `json:"run_at,omitempty"`
	NextRunAt string `json:"next_run_at,omitempty"`
	Status    string `json:"status"`
	Message      string `json:"message"`
	RunCount     int    `json:"run_count"`
	Paused       bool   `json:"paused,omitempty"`
	IntervalMs   int64  `json:"interval_ms,omitempty"`
	Cron         string `json:"cron,omitempty"`
	MaxRuns      int    `json:"max_runs,omitempty"`
	EndsAt       string `json:"ends_at,omitempty"`
	LastRunAt    string `json:"last_run_at,omitempty"`
	LastError    string `json:"last_error,omitempty"`

	// Target scope. SessionMode is always present so the UI can render the
	// scope without inferring it; the project fields are empty for a
	// session-scoped row.
	SessionMode      string `json:"session_mode"`
	ProjectID        string `json:"project_id,omitempty"`
	ProjectName      string `json:"project_name,omitempty"`
	SessionTemplate  string `json:"session_template,omitempty"`
	LastSessionID    string `json:"last_session_id,omitempty"`
	LastSessionLabel string `json:"last_session_label,omitempty"`
	// SourceSessionID is the session the schedule was created from — for a
	// project job it is the only link back to that conversation.
	SourceSessionID string `json:"source_session_id,omitempty"`
	// ManualRuns is fires triggered by Run now. Separate from RunCount so
	// "ran 3x" stays the count of SCHEDULED fires (what MaxRuns caps).
	ManualRuns int `json:"manual_runs,omitempty"`
	// CronTimezone names the zone a cron expression is matched in (the
	// server's), so the UI never has to guess whether 9am is local or UTC.
	CronTimezone string `json:"cron_timezone,omitempty"`
}

func scheduleToVM(m entity.ScheduledMessage) scheduleVM {
	vm := scheduleVM{
		ID:              m.ID,
		SessionID:       m.SessionID,
		CreatedBy:       m.CreatedBy,
		Kind:            m.Kind,
		Status:          m.EffectiveStatus(),
		Message:         m.Message,
		RunCount:        m.RunCount,
		LastError:       m.LastError,
		SessionMode:     m.Mode(),
		ProjectID:       m.ProjectID,
		SessionTemplate: m.SessionTemplate,
		LastSessionID:   m.LastSessionID,
		SourceSessionID: m.SourceSessionID,
		ManualRuns:      m.ManualRuns,
	}
	if m.Cron != "" {
		vm.CronTimezone = schedule.ServerZoneLabel(m.RunAt)
	}
	// Only a live row has a next fire. A finished/mid-claim row's stored
	// run_at is the claim park sentinel (~100 years out), which must never
	// reach the UI — it would render "next run 2126" and sort first.
	if next := m.NextRunAt(); next != nil {
		vm.RunAt = next.UTC().Format(time.RFC3339)
		vm.NextRunAt = vm.RunAt
	}
	if m.ProjectID != "" {
		if p, err := project.Load(globalLayout, m.ProjectID); err == nil {
			vm.ProjectName = p.Meta.Name
		}
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

// scheduleBelongsToSession reports whether a schedule is one this session's
// panel may act on. It must admit exactly what the panel LISTS (see
// Store.ListFiltered), otherwise a row shows up with buttons that 404.
//
// A session-scoped row must target this session. A project-scoped row
// qualifies when it belongs to this session's project — a project job is owned
// by the project, so any session in it can manage the job — or, as a fallback
// for a session with no project binding, when it was created from here.
func scheduleBelongsToSession(m entity.ScheduledMessage, sid, sessionProjectID string) bool {
	if m.SessionID == sid {
		return true
	}
	if !m.IsProjectScoped() {
		return false
	}
	if sessionProjectID != "" && m.ProjectID == sessionProjectID {
		return true
	}
	return m.SourceSessionID == sid
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

// sessionSchedulesListUI returns this session's schedules (owner/admin only):
// the ones aimed at it, plus the project jobs of the project it belongs to —
// those are owned by the project, so every session in it sees them.
func sessionSchedulesListUI(c *tool.Ctx) {
	sid := c.PathValue("id")
	sess, ok := scheduleResolveSession(c, sid)
	if !ok {
		return
	}
	// Scope to this session; owner filter is implied by the ownership check
	// above (a caller who owns/sees this session sees its schedules).
	scope := schedule.SessionScope{ID: sid, ProjectID: sess.Meta.ProjectID}
	rows, err := globalSchedule.ListFiltered(c.Context(), "", scope, "", true)
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
		// Target scope. Omitted → a plain nudge into this session. Set to
		// run in a project instead, with a session minted per fire.
		ProjectID       string `json:"project_id"`
		SessionMode     string `json:"session_mode"`
		SessionTemplate string `json:"session_template"`
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

	// The panel lives inside a session, so the session is the default target;
	// naming a project (or a non-default mode) switches the row to
	// project scope, where each fire gets its own session.
	target := schedule.NormalizeTargetSpec(schedule.TargetSpec{
		SessionID: sid,
		ProjectID: body.ProjectID,
		Mode:      body.SessionMode,
		Template:  body.SessionTemplate,
	})
	if target.Mode != entity.ScheduledSessionExisting {
		// A project-scoped row runs work inside that project, so require the
		// caller to actually have access to it.
		if !callerProjectAccess(c).allowProject(target.ProjectID) {
			c.Error(http.StatusNotFound, "project not found")
			return
		}
		target.SessionID = "" // the target is resolved per fire
	}
	if err := schedule.ValidateTargetSpec(target, time.Now()); err != nil {
		c.Error(http.StatusBadRequest, err.Error())
		return
	}

	row := &entity.ScheduledMessage{
		SessionID:       target.SessionID,
		ProjectID:       target.ProjectID,
		SessionMode:     target.Mode,
		SessionTemplate: target.Template,
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
	sess, ok := scheduleResolveSession(c, sid)
	if !ok {
		return
	}
	scheduleID := c.PathValue("sid")
	m, err := globalSchedule.Get(c.Context(), scheduleID)
	if err != nil || !scheduleBelongsToSession(*m, sid, sess.Meta.ProjectID) {
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
		if patch, err = scheduleParsePatchUI(*m, c, sid, time.Now()); err == nil {
			err = globalSchedule.Reschedule(c.Context(), scheduleID, patch)
		}
	case "run_now":
		// Make it due now and poke the runner, so the fire lands in seconds
		// rather than at the next poll. The schedule is unchanged.
		if err = globalSchedule.RunNow(c.Context(), scheduleID); err == nil {
			schedule.WakeRunner()
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
// Timing, message, cap, and (for project-scoped rows) the target can all be
// edited; scope itself cannot change — see scheduleTargetPatchUI.
func scheduleParsePatchUI(m entity.ScheduledMessage, c *tool.Ctx, sessionID string, now time.Time) (schedule.SchedulePatch, error) {
	var body struct {
		RunAt   string `json:"run_at"`
		Every   string `json:"every"`
		Cron    string `json:"cron"`
		Message string `json:"message"`
		MaxRuns *int   `json:"max_runs"`
		// Target edits. Pointers so "field absent" is distinguishable from
		// "field cleared" — only what the client sent is touched.
		ProjectID       *string `json:"project_id"`
		SessionMode     *string `json:"session_mode"`
		SessionTemplate *string `json:"session_template"`
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
	if err := scheduleTargetPatchUI(m, c, sessionID, now, body.ProjectID, body.SessionMode, body.SessionTemplate, &patch); err != nil {
		return patch, err
	}
	return patch, nil
}

// scheduleTargetPatchUI folds target edits into the patch: repoint a
// project-scoped schedule at another project, switch new↔template, fix a
// pattern, or move the schedule between session and project scope.
//
// A scope move re-homes the row, so the owner is re-stamped to match the new
// scope — otherwise the schedule keeps the old scope's owner and disappears
// from its own listings. Either way the new target must be one the caller can
// actually reach, or reschedule would become a way to run work inside someone
// else's project.
//
// sessionID is the session this edit is being made from; it becomes the target
// when moving to session scope, since the UI has no other session to mean.
func scheduleTargetPatchUI(
	m entity.ScheduledMessage,
	c *tool.Ctx,
	sessionID string,
	now time.Time,
	projectID, sessionMode, sessionTemplate *string,
	patch *schedule.SchedulePatch,
) error {
	if projectID == nil && sessionMode == nil && sessionTemplate == nil {
		return nil
	}
	// Overlay only what was sent onto the row's current target, so editing
	// the pattern alone keeps the project and mode.
	next := schedule.TargetSpec{
		SessionID: m.SessionID,
		ProjectID: m.ProjectID,
		Mode:      m.Mode(),
		Template:  m.SessionTemplate,
	}
	if projectID != nil {
		next.ProjectID = strings.TrimSpace(*projectID)
	}
	if sessionMode != nil {
		next.Mode = strings.TrimSpace(*sessionMode)
	} else if sessionTemplate != nil && strings.TrimSpace(*sessionTemplate) != "" && m.IsProjectScoped() {
		next.Mode = entity.ScheduledSessionTemplate
	}
	if sessionTemplate != nil {
		next.Template = strings.TrimSpace(*sessionTemplate)
	}
	if next.Mode == entity.ScheduledSessionExisting {
		// Moving to session scope: target the session this edit came from
		// (the row may have had none) and drop the project config.
		if next.SessionID == "" {
			next.SessionID = sessionID
		}
		next.ProjectID, next.Template = "", ""
	} else {
		next.SessionID = "" // resolved per fire
	}
	if err := schedule.ValidateTargetSpec(next, now); err != nil {
		return err
	}

	if next.Mode == entity.ScheduledSessionExisting {
		sess, err := session.Load(globalLayout, next.SessionID)
		if err != nil || !ownsSession(c, sess) {
			return fmt.Errorf("session not found")
		}
		owner := sess.Meta.UserID
		patch.OwnerUserID = &owner
	} else {
		if !callerProjectAccess(c).allowProject(next.ProjectID) {
			return fmt.Errorf("project not found")
		}
		if p, err := project.Load(globalLayout, next.ProjectID); err == nil {
			owner := p.Meta.OwnerUserID
			if owner == "" {
				owner = m.OwnerUserID // ownerless/shared project: keep as-is
			}
			patch.OwnerUserID = &owner
		}
	}

	patch.ProjectID = &next.ProjectID
	patch.SessionMode = &next.Mode
	patch.SessionTemplate = &next.Template
	patch.SessionID = &next.SessionID
	return nil
}

// sessionSchedulesCancelUI cancels a pending schedule on this session.
func sessionSchedulesCancelUI(c *tool.Ctx) {
	sid := c.PathValue("id")
	sess, ok := scheduleResolveSession(c, sid)
	if !ok {
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
	if err != nil || !scheduleBelongsToSession(*m, sid, sess.Meta.ProjectID) {
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
		vm, ok := scheduleMonitorVM(m, sessions, access)
		if !ok {
			continue
		}
		out = append(out, vm)
	}
	c.JSON(http.StatusOK, map[string]any{"schedules": out})
}

// scheduleMonitorVM builds one monitor row and decides whether the caller may
// see it, branching on scope:
//
//   - session-scoped: gate on the target session (allowSession). A session
//     that has left the registry is hidden rather than shown as an orphan the
//     caller can't verify access to.
//   - project-scoped: gate on the project (allowProject). There is no target
//     session to check — the schedule creates one per fire — so the project IS
//     the access boundary. Its last run's session supplies the label/link.
func scheduleMonitorVM(m entity.ScheduledMessage, sessions map[string]session.Session, access projectAccess) (scheduleVM, bool) {
	if m.IsProjectScoped() {
		if !access.allowProject(m.ProjectID) {
			return scheduleVM{}, false
		}
		vm := scheduleToVM(m)
		if last, ok := sessions[m.LastSessionID]; ok {
			vm.LastSessionLabel = firstNonEmptyStr(last.Meta.Label, m.LastSessionID)
		}
		return vm, true
	}
	sess, ok := sessions[m.SessionID]
	if !ok {
		return scheduleVM{}, false
	}
	if !access.allowSession(sess.Meta.ProjectID, sess.Meta.UserID) {
		return scheduleVM{}, false
	}
	vm := scheduleToVM(m)
	vm.SessionLabel = firstNonEmptyStr(sess.Meta.Label, m.SessionID)
	return vm, true
}

// scheduleByIDMutateUI runs cancel/pause/resume/reschedule on a schedule
// addressed by id alone (the global page has no session in the path). Access
// uses the same scope-aware rule as the monitor listing, so a row the page
// can show is a row the page can act on.
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
	if _, ok := scheduleMonitorVM(*m, globalMgr.Registry().Sessions(), callerProjectAccess(c)); !ok {
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
	case "reschedule":
		var patch schedule.SchedulePatch
		// The global page has no session in the path, so "" is passed as the
		// move-to-session target: moving a job INTO session scope from here
		// has no session to mean, and scheduleTargetPatchUI rejects it.
		if patch, err = scheduleParsePatchUI(*m, c, "", time.Now()); err == nil {
			err = globalSchedule.Reschedule(c.Context(), scheduleID, patch)
		}
	case "run_now":
		if err = globalSchedule.RunNow(c.Context(), scheduleID); err == nil {
			schedule.WakeRunner()
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
