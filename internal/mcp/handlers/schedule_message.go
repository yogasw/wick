package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/schedule"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

const scheduleToolName = "wick_schedule_message"

// scheduleMaxMessageRunes caps a scheduled message so a runaway prompt
// can't bloat the row / the eventual turn.
const scheduleMaxMessageRunes = 8000

// WickScheduleMessage handles the wick_schedule_message tool — the
// agent-facing surface for scheduling a future message into a session.
//
// An agent can schedule itself ("check back at 12:40"): pass the current
// session_id, a run_at, and the message the agent should receive then. When
// the time comes, the schedule runner injects the message as a normal
// role=user turn through the pool, so it spawns the session if idle or
// queues behind an in-flight turn if busy — identical to any inbound
// message. No workflow engine involved.
//
// Access: create/list/cancel are gated to the target session's owner (or an
// admin) via canManageSession. Every row records owner_user_id +
// source_session_id + created_by so the dashboard shows who asked for it.
//
// Actions:
//   - create: schedule a message. args: session_id, run_at, message, [agent_name].
//   - list:   list schedules the caller may see. args: [session_id].
//   - cancel: cancel a pending schedule. args: id.
func WickScheduleMessage(
	w http.ResponseWriter,
	r *http.Request,
	req RPCRequest,
	rsp Responder,
	store *schedule.Store,
	layout agentconfig.Layout,
	args map[string]any,
	user *entity.User,
) {
	if store == nil {
		rsp.ToolError(w, req.ID, "scheduling is unavailable on this transport", scheduleToolName)
		return
	}
	action := strings.TrimSpace(argString(args, "action"))
	switch action {
	case "create":
		scheduleCreate(w, r, req, rsp, store, layout, args, user)
	case "list":
		scheduleList(w, r, req, rsp, store, layout, args, user)
	case "cancel":
		scheduleMutate(w, r, req, rsp, store, args, user, "cancel")
	case "pause":
		scheduleMutate(w, r, req, rsp, store, args, user, "pause")
	case "resume":
		scheduleMutate(w, r, req, rsp, store, args, user, "resume")
	case "reschedule":
		scheduleMutate(w, r, req, rsp, store, args, user, "reschedule")
	default:
		rsp.ToolError(w, req.ID, "action must be one of: create, list, cancel, pause, resume, reschedule", scheduleToolName)
	}
}

func scheduleCreate(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, store *schedule.Store, layout agentconfig.Layout, args map[string]any, user *entity.User) {
	sessionID := strings.TrimSpace(argString(args, "session_id"))
	if sessionID == "" {
		rsp.ToolError(w, req.ID, "session_id is required (the session to deliver into)", scheduleToolName)
		return
	}
	message := strings.TrimSpace(argString(args, "message"))
	if message == "" {
		rsp.ToolError(w, req.ID, "message is required", scheduleToolName)
		return
	}
	if runes := []rune(message); len(runes) > scheduleMaxMessageRunes {
		rsp.ToolError(w, req.ID, fmt.Sprintf("message too long (max %d characters)", scheduleMaxMessageRunes), scheduleToolName)
		return
	}
	// Timing: run_at (one-shot) OR every (interval) OR cron (recurring).
	spec, err := schedule.ParseWhen(argString(args, "run_at"), argString(args, "every"), argString(args, "cron"), time.Now())
	if err != nil {
		rsp.ToolError(w, req.ID, err.Error(), scheduleToolName)
		return
	}

	sess, err := session.Load(layout, sessionID)
	if err != nil {
		rsp.ToolError(w, req.ID, "load session: "+err.Error(), scheduleToolName)
		return
	}
	if !canManageSession(user, sess.Meta.UserID) {
		// Match the title tools: don't leak that the session exists.
		rsp.ToolError(w, req.ID, fmt.Sprintf("session not found: %s", sessionID), scheduleToolName)
		return
	}

	// This tool is agent-facing, so default provenance is "ai". A caller
	// can override (e.g. an external cron identifying itself as "api"); the
	// dashboard's own create path stamps "user" directly on the store.
	createdBy := entity.ScheduledByAI
	if v := strings.TrimSpace(argString(args, "created_by")); v == entity.ScheduledByUser || v == entity.ScheduledByAPI {
		createdBy = v
	}

	row := &entity.ScheduledMessage{
		SessionID:       sessionID,
		OwnerUserID:     sess.Meta.UserID,
		CreatedBy:       createdBy,
		SourceSessionID: sessionID,
		AgentName:       strings.TrimSpace(argString(args, "agent_name")),
		Message:         message,
		RunAt:           spec.FirstRunAt,
		MaxRuns:         argInt(args, "max_runs"),
	}
	if spec.Recurring {
		row.Kind = entity.ScheduledKindRecurring
		row.IntervalMs = spec.IntervalMs
		row.Cron = spec.Cron
	}
	m, err := store.Create(r.Context(), row)
	if err != nil {
		rsp.ToolError(w, req.ID, "create schedule: "+err.Error(), scheduleToolName)
		return
	}
	writeScheduleResult(w, req, rsp, map[string]any{
		"schedule": scheduleVM(*m),
		"note":     scheduleCreateNote(*m),
	})
}

func scheduleCreateNote(m entity.ScheduledMessage) string {
	if m.IsRecurring() {
		return "Scheduled to repeat. It fires as a normal message into the session each time. Pause with action=pause, stop with action=cancel, change timing with action=reschedule — all by id=" + m.ID + "."
	}
	return "Scheduled. It fires once and is delivered as a normal message into the session. Cancel with action=cancel id=" + m.ID + "."
}

func scheduleList(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, store *schedule.Store, layout agentconfig.Layout, args map[string]any, user *entity.User) {
	sessionID := strings.TrimSpace(argString(args, "session_id"))
	// When a session is named, verify the caller may see it first so a
	// non-owner can't enumerate someone else's schedules by session id.
	if sessionID != "" {
		sess, err := session.Load(layout, sessionID)
		if err != nil {
			rsp.ToolError(w, req.ID, "load session: "+err.Error(), scheduleToolName)
			return
		}
		if !canManageSession(user, sess.Meta.UserID) {
			rsp.ToolError(w, req.ID, fmt.Sprintf("session not found: %s", sessionID), scheduleToolName)
			return
		}
	}
	ownerID, allOwners := scheduleScope(user)
	rows, err := store.ListForOwner(r.Context(), ownerID, sessionID, allOwners)
	if err != nil {
		rsp.ToolError(w, req.ID, "list schedules: "+err.Error(), scheduleToolName)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, m := range rows {
		out = append(out, scheduleVM(m))
	}
	writeScheduleResult(w, req, rsp, map[string]any{"schedules": out})
}

// scheduleMutate handles the by-id lifecycle actions: cancel, pause, resume,
// reschedule. All resolve the schedule, enforce owner/admin access, apply the
// transition, and return the fresh row.
func scheduleMutate(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, store *schedule.Store, args map[string]any, user *entity.User, action string) {
	id := strings.TrimSpace(argString(args, "id"))
	if id == "" {
		rsp.ToolError(w, req.ID, "id is required (the schedule to "+action+")", scheduleToolName)
		return
	}
	m, err := store.Get(r.Context(), id)
	if err != nil {
		rsp.ToolError(w, req.ID, "schedule not found: "+id, scheduleToolName)
		return
	}
	if !canManageSession(user, m.OwnerUserID) {
		rsp.ToolError(w, req.ID, "schedule not found: "+id, scheduleToolName)
		return
	}

	switch action {
	case "cancel":
		err = store.Cancel(r.Context(), id)
	case "pause":
		if !m.IsRecurring() {
			rsp.ToolError(w, req.ID, "only recurring schedules can be paused", scheduleToolName)
			return
		}
		err = store.SetPaused(r.Context(), id, true, time.Time{})
	case "resume":
		if !m.IsRecurring() {
			rsp.ToolError(w, req.ID, "only recurring schedules can be resumed", scheduleToolName)
			return
		}
		var next time.Time
		if next, err = schedule.NextFrom(*m, time.Now()); err == nil {
			err = store.SetPaused(r.Context(), id, false, next)
		}
	case "reschedule":
		var patch schedule.SchedulePatch
		if patch, err = scheduleParsePatch(*m, args, time.Now()); err == nil {
			err = store.Reschedule(r.Context(), id, patch)
		}
	}
	if err != nil {
		if err == schedule.ErrNotFound {
			rsp.ToolError(w, req.ID, action+": schedule is not in a state that allows it", scheduleToolName)
			return
		}
		rsp.ToolError(w, req.ID, action+": "+err.Error(), scheduleToolName)
		return
	}
	fresh, _ := store.Get(r.Context(), id)
	writeScheduleResult(w, req, rsp, map[string]any{"schedule": scheduleVM(*fresh)})
}

// scheduleParsePatch builds a store patch from reschedule args. Only supplied
// fields change; timing re-uses ParseWhen so the same run_at/every/cron
// grammar applies. Kind cannot change (one-shot stays one-shot).
func scheduleParsePatch(m entity.ScheduledMessage, args map[string]any, now time.Time) (schedule.SchedulePatch, error) {
	var patch schedule.SchedulePatch
	runAt := strings.TrimSpace(argString(args, "run_at"))
	every := strings.TrimSpace(argString(args, "every"))
	cron := strings.TrimSpace(argString(args, "cron"))
	if runAt != "" || every != "" || cron != "" {
		spec, err := schedule.ParseWhen(runAt, every, cron, now)
		if err != nil {
			return patch, err
		}
		if spec.Recurring != m.IsRecurring() {
			return patch, fmt.Errorf("cannot change a %s schedule into the other kind; cancel and create a new one", m.Kind)
		}
		patch.RunAt = spec.FirstRunAt
		if spec.Recurring {
			iv := spec.IntervalMs
			cr := spec.Cron
			patch.IntervalMs = &iv
			patch.Cron = &cr
		}
	}
	if msg, ok := args["message"].(string); ok {
		msg = strings.TrimSpace(msg)
		if msg != "" {
			patch.Message = &msg
		}
	}
	if _, ok := args["max_runs"]; ok {
		mr := argInt(args, "max_runs")
		patch.MaxRuns = &mr
	}
	return patch, nil
}

// argInt reads an integer arg (JSON numbers arrive as float64). Missing or
// non-numeric → 0.
func argInt(args map[string]any, key string) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

// scheduleScope returns (ownerID, allOwners) for a list query. Only the app
// super-user (CanSeeAllSessions / IsOwner) enumerates every owner's
// schedules; a plain admin is scoped to their own, matching the "admins
// don't see everything by default" rule the UI monitor enforces via
// admin_see_all. A cross-user view for admins is the UI monitor's job (it
// reads the admin_see_all config, which this transport does not carry).
// nil user (stdio / tests) is unscoped so local tooling sees everything.
func scheduleScope(user *entity.User) (string, bool) {
	if user == nil || user.CanSeeAllSessions() {
		return "", true
	}
	return user.ID, false
}

func scheduleVM(m entity.ScheduledMessage) map[string]any {
	vm := map[string]any{
		"id":         m.ID,
		"session_id": m.SessionID,
		"created_by": m.CreatedBy,
		"kind":       m.Kind,
		"run_at":     m.RunAt.UTC().Format(time.RFC3339),
		"status":     m.Status,
		"message":    m.Message,
		"run_count":  m.RunCount,
	}
	if m.IsRecurring() {
		vm["paused"] = m.Paused
		if m.IntervalMs > 0 {
			vm["interval_ms"] = m.IntervalMs
		}
		if m.Cron != "" {
			vm["cron"] = m.Cron
		}
		if m.MaxRuns > 0 {
			vm["max_runs"] = m.MaxRuns
		}
		if m.EndsAt != nil {
			vm["ends_at"] = m.EndsAt.UTC().Format(time.RFC3339)
		}
	}
	if m.LastRunAt != nil {
		vm["last_run_at"] = m.LastRunAt.UTC().Format(time.RFC3339)
	}
	if m.LastError != "" {
		vm["last_error"] = m.LastError
	}
	return vm
}

func writeScheduleResult(w http.ResponseWriter, req RPCRequest, rsp Responder, out map[string]any) {
	b, _ := json.Marshal(out)
	rsp.WriteResult(w, req.ID, ToolCallResult{
		Content: []ToolContent{{Type: "text", Text: string(b)}},
	})
}
