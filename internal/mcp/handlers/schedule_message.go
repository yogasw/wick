package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/schedule"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

const scheduleToolName = "wick_schedule_message"

// scheduleMaxMessageRunes caps a scheduled message so a runaway prompt
// can't bloat the row / the eventual turn.
const scheduleMaxMessageRunes = 8000

// WickScheduleMessage handles the wick_schedule_message tool — the
// agent-facing surface for scheduling a future message.
//
// Two target shapes, chosen with session_id vs project_id:
//
//   - session_id → nudge a conversation that already exists ("check back at
//     12:40"). Every fire lands in that same session.
//   - project_id → a standalone job in a project ("every Monday 9am, write
//     the weekly report"). session_mode decides where each fire lands: "new"
//     (default for project scope) opens a fresh session per run, so the run
//     starts with clean context; "template" renders session_template against
//     the fire time and reuses that session when it already exists.
//
// Either way the runner injects the message as a normal role=user turn
// through the pool, so it spawns the session if idle or queues behind an
// in-flight turn if busy — identical to any inbound message. No workflow
// engine involved.
//
// Access: session-scoped rows are gated on the target session's owner via
// canManageSession; project-scoped rows on project.CanAccess. Every row
// records owner_user_id + source_session_id + created_by so the dashboard
// shows who asked for it.
//
// Actions:
//   - create:     schedule a message. args: session_id | project_id
//     [+session_mode, session_template], run_at | every | cron, message,
//     [agent_name, max_runs].
//   - list:       list schedules the caller may see. args: [session_id, project_id].
//   - cancel:     stop a schedule. args: id.
//   - pause:      suspend a recurring schedule. args: id.
//   - resume:     un-suspend it, recomputing the next fire. args: id.
//   - reschedule: edit timing, message, and/or target. args: id + fields to change.
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
		scheduleMutate(w, r, req, rsp, store, layout, args, user, "cancel")
	case "pause":
		scheduleMutate(w, r, req, rsp, store, layout, args, user, "pause")
	case "resume":
		scheduleMutate(w, r, req, rsp, store, layout, args, user, "resume")
	case "reschedule":
		scheduleMutate(w, r, req, rsp, store, layout, args, user, "reschedule")
	case "run_now":
		scheduleMutate(w, r, req, rsp, store, layout, args, user, "run_now")
	default:
		rsp.ToolError(w, req.ID, "action must be one of: create, list, cancel, pause, resume, reschedule, run_now", scheduleToolName)
	}
}

func scheduleCreate(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, store *schedule.Store, layout agentconfig.Layout, args map[string]any, user *entity.User) {
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

	// Target: session-scoped (nudge one session) or project-scoped (mint or
	// reuse a session per fire). The mode is inferred when the caller only
	// names one of session_id / project_id.
	target := schedule.NormalizeTargetSpec(schedule.TargetSpec{
		SessionID: argString(args, "session_id"),
		ProjectID: argString(args, "project_id"),
		Mode:      argString(args, "session_mode"),
		Template:  argString(args, "session_template"),
	})
	if err := schedule.ValidateTargetSpec(target, time.Now()); err != nil {
		rsp.ToolError(w, req.ID, err.Error(), scheduleToolName)
		return
	}

	// Ownership follows the scope: a session-scoped row belongs to the target
	// session's owner, a project-scoped row to the project's owner (falling
	// back to the caller when the project is ownerless/shared).
	ownerUserID, aerr := scheduleAuthorizeTarget(r, layout, target, user)
	if aerr != nil {
		rsp.ToolError(w, req.ID, aerr.Error(), scheduleToolName)
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
		SessionID:       target.SessionID,
		ProjectID:       target.ProjectID,
		SessionMode:     target.Mode,
		SessionTemplate: target.Template,
		OwnerUserID:     ownerUserID,
		CreatedBy:       createdBy,
		SourceSessionID: scheduleSourceSession(args, target),
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
	where := "into the session"
	switch m.Mode() {
	case entity.ScheduledSessionNew:
		where = "into a NEW session in project " + m.ProjectID + " (fresh context every run)"
	case entity.ScheduledSessionTemplate:
		// Show the RENDERED name, not the raw pattern: a typo in the pattern
		// is only obvious once you see what it actually produces, and waiting
		// for the first fire to find out is the expensive way.
		rendered, err := schedule.RenderTemplate(m.SessionTemplate, m, time.Now())
		if err != nil {
			rendered = m.SessionTemplate
		}
		where = "into session " + m.SessionTemplate + " in project " + m.ProjectID +
			" — the next fire resolves to \"" + rendered + "\" (created on first use, reused after)"
	}
	tail := " Fire it immediately with action=run_now id=" + m.ID +
		" — a manual run does NOT count toward max_runs and does NOT shift the next fire, so it is safe for testing."
	if m.Cron != "" {
		// Name the zone the expression was interpreted in. It is the server's
		// local zone, and getting it wrong puts a "9am report" hours off — so
		// state it in the result instead of making the caller probe for it.
		tail += " Cron is interpreted in the server timezone " + schedule.ServerZoneLabel(m.RunAt) + "."
	}
	if m.IsRecurring() {
		return "Scheduled to repeat. It fires as a normal message " + where + " each time. Pause with action=pause, stop with action=cancel, change timing or target with action=reschedule — all by id=" + m.ID + "." + tail
	}
	return "Scheduled. It fires once and is delivered as a normal message " + where + ". Cancel with action=cancel id=" + m.ID + "." + tail
}

func scheduleList(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, store *schedule.Store, layout agentconfig.Layout, args map[string]any, user *entity.User) {
	sessionID := strings.TrimSpace(argString(args, "session_id"))
	// When a session is named, verify the caller may see it first so a
	// non-owner can't enumerate someone else's schedules by session id.
	// The session's own project rides along so the listing also returns the
	// project jobs of the project it belongs to — those are owned by the
	// project, not by whichever session created them.
	var scope schedule.SessionScope
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
		scope = schedule.SessionScope{ID: sessionID, ProjectID: sess.Meta.ProjectID}
	}
	// Same for a named project: prove visibility before it can be used as a
	// filter, so the list can't enumerate someone else's project.
	projectID := strings.TrimSpace(argString(args, "project_id"))
	if projectID != "" {
		p, err := project.Load(layout, projectID)
		if err != nil || !project.CanAccess(p.Meta, scheduleProjectAccess(r, user)) {
			rsp.ToolError(w, req.ID, fmt.Sprintf("project not found: %s", projectID), scheduleToolName)
			return
		}
	}
	// The strict variant: only what delivers INTO this session. Access-checked
	// the same way, so it can't be used to probe another user's sessions.
	targetSessionID := strings.TrimSpace(argString(args, "target_session_id"))
	if targetSessionID != "" && targetSessionID != sessionID {
		sess, err := session.Load(layout, targetSessionID)
		if err != nil || !canManageSession(user, sess.Meta.UserID) {
			rsp.ToolError(w, req.ID, fmt.Sprintf("session not found: %s", targetSessionID), scheduleToolName)
			return
		}
	}

	ownerID, allOwners := scheduleScope(user)
	statuses, paused := scheduleListStatuses(args)
	q := schedule.ListQuery{
		OwnerUserID: ownerID,
		Scope:       scope,
		ProjectID:   projectID,
		AllOwners:   allOwners,
		Statuses:        statuses,
		Paused:          paused,
		TargetSessionID: targetSessionID,
		Limit:           scheduleListLimit(args),
	}
	rows, err := store.List(r.Context(), q)
	if err != nil {
		rsp.ToolError(w, req.ID, "list schedules: "+err.Error(), scheduleToolName)
		return
	}
	// Listings truncate the message: a list of 30 schedules carrying full
	// 1500-character prompts is mostly noise to whoever asked "what is
	// scheduled?", and it crowds out the rest of the context. The full text
	// is one get away — any by-id action returns it whole.
	out := make([]map[string]any, 0, len(rows))
	for _, m := range rows {
		vm := scheduleVM(m)
		if preview, truncated := truncateRunes(m.Message, scheduleListMessageRunes); truncated {
			delete(vm, "message")
			vm["message_preview"] = preview
			vm["message_truncated"] = true
		}
		out = append(out, vm)
	}
	res := map[string]any{"schedules": out}
	if len(rows) == q.Limit {
		res["note"] = fmt.Sprintf("Result capped at %d rows — narrow with session_id / project_id / status, or raise limit.", q.Limit)
	}
	writeScheduleResult(w, req, rsp, res)
}

// scheduleListMessageRunes caps the message text a LIST returns per row.
const scheduleListMessageRunes = 200

// scheduleListDefaultLimit is what a `list` returns when the caller doesn't
// say. Small enough to stay cheap in a tool result, large enough to cover a
// realistic set of live schedules.
const scheduleListDefaultLimit = 50

// scheduleListStatuses reads the status filter. Default is live-only
// (pending + active): "list my schedules" almost always means "what is still
// going to happen", not a full audit history of everything ever cancelled.
// Pass status="all" for every status, or a comma-separated subset.
// Returns the stored statuses to match, plus a paused filter when the caller
// asked for "paused" specifically — that status is reported but never stored
// (a paused row is `active` with paused=true), so it has to be translated.
func scheduleListStatuses(args map[string]any) ([]string, *bool) {
	raw := strings.TrimSpace(argString(args, "status"))
	if raw == "" {
		return schedule.LiveStatuses(), nil
	}
	if strings.EqualFold(raw, "all") {
		return nil, nil
	}
	var out []string
	onlyPaused := false
	for _, part := range strings.Split(raw, ",") {
		p := strings.TrimSpace(part)
		if p == "" {
			continue
		}
		if strings.EqualFold(p, entity.ScheduledStatusPaused) {
			// "paused" alone means "live but suspended"; alongside other
			// statuses it just widens the live set, since a paused row is
			// stored as active.
			onlyPaused = len(out) == 0
			out = append(out, entity.ScheduledStatusActive)
			continue
		}
		out = append(out, p)
		onlyPaused = false
	}
	if len(out) == 0 {
		return schedule.LiveStatuses(), nil
	}
	if onlyPaused {
		yes := true
		return out, &yes
	}
	return out, nil
}

func scheduleListLimit(args map[string]any) int {
	if n := argInt(args, "limit"); n > 0 {
		if n > 500 {
			return 500
		}
		return n
	}
	return scheduleListDefaultLimit
}

// truncateRunes cuts s to at most n runes, reporting whether it had to.
func truncateRunes(s string, n int) (string, bool) {
	r := []rune(s)
	if len(r) <= n {
		return s, false
	}
	return string(r[:n]) + "…", true
}

// scheduleMutate handles the by-id lifecycle actions: cancel, pause, resume,
// reschedule. All resolve the schedule, enforce owner/admin access, apply the
// transition, and return the fresh row.
func scheduleMutate(w http.ResponseWriter, r *http.Request, req RPCRequest, rsp Responder, store *schedule.Store, layout agentconfig.Layout, args map[string]any, user *entity.User, action string) {
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
	if !scheduleCanManage(r, layout, *m, user) {
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
		if patch, err = scheduleParsePatch(r, layout, user, *m, args, time.Now()); err == nil {
			err = store.Reschedule(r.Context(), id, patch)
		}
	case "run_now":
		// Make it due and poke the runner, so the fire lands in seconds
		// instead of at the next poll. The schedule itself is unchanged: a
		// recurring row advances from this fire like any other.
		if err = store.RunNow(r.Context(), id); err == nil {
			schedule.WakeRunner()
		}
	}
	if err != nil {
		if err == schedule.ErrNotFound {
			// Name the actual state: "not in a state that allows it" left the
			// caller to guess whether it was done, cancelled, or the wrong kind.
			rsp.ToolError(w, req.ID, fmt.Sprintf("%s: schedule is %s — only a live (pending/active) schedule can be changed",
				action, m.EffectiveStatus()), scheduleToolName)
			return
		}
		rsp.ToolError(w, req.ID, action+": "+err.Error(), scheduleToolName)
		return
	}
	fresh, _ := store.Get(r.Context(), id)
	out := map[string]any{"schedule": scheduleVM(*fresh)}
	if action == "run_now" {
		// Spell it out: the fire is extra, and next_run_at below is still the
		// schedule's own next fire, unchanged. Without this an agent reading
		// the response can conclude the schedule was disturbed and try to
		// "repair" something that isn't broken.
		note := "Firing now as an EXTRA run. The schedule is unchanged: next_run_at is still its own next fire, " +
			"run_count and max_runs are untouched (the manual fire counts under manual_runs). " +
			"Delivery is polled, so it lands within a few seconds."
		if next := fresh.NextRunAt(); next != nil {
			note += " Next scheduled fire remains " + next.UTC().Format(time.RFC3339) + "."
		}
		out["note"] = note
	}
	writeScheduleResult(w, req, rsp, out)
}

// scheduleParsePatch builds a store patch from reschedule args. Only supplied
// fields change; timing re-uses ParseWhen so the same run_at/every/cron
// grammar applies. Kind cannot change (one-shot stays one-shot), and neither
// can scope (see scheduleTargetPatch).
func scheduleParsePatch(r *http.Request, layout agentconfig.Layout, user *entity.User, m entity.ScheduledMessage, args map[string]any, now time.Time) (schedule.SchedulePatch, error) {
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
	if err := scheduleTargetPatch(r, layout, user, m, args, now, &patch); err != nil {
		return patch, err
	}
	return patch, nil
}

// scheduleTargetPatch folds target edits into a reschedule patch: repoint a
// project-scoped schedule at another project, switch new↔template, fix a
// pattern — or move the schedule between session and project scope.
//
// A scope move re-homes the row, so it is authorized like a fresh create
// against the NEW target (scheduleAuthorizeTarget) and the resulting owner is
// re-stamped onto the row. Without that re-stamp the schedule would keep the
// old scope's owner and drop out of its own listings.
func scheduleTargetPatch(r *http.Request, layout agentconfig.Layout, user *entity.User, m entity.ScheduledMessage, args map[string]any, now time.Time, patch *schedule.SchedulePatch) error {
	_, hasProject := args["project_id"]
	_, hasMode := args["session_mode"]
	_, hasTemplate := args["session_template"]
	_, hasSession := args["session_id"]
	if !hasProject && !hasMode && !hasTemplate && !hasSession {
		return nil
	}

	// Start from the row's current target and overlay only what was supplied,
	// so a lone session_template edit keeps the existing project and mode.
	next := schedule.TargetSpec{
		SessionID: m.SessionID,
		ProjectID: m.ProjectID,
		Mode:      m.Mode(),
		Template:  m.SessionTemplate,
	}
	if hasProject {
		next.ProjectID = strings.TrimSpace(argString(args, "project_id"))
	}
	if hasSession {
		next.SessionID = strings.TrimSpace(argString(args, "session_id"))
	}
	if hasMode {
		next.Mode = strings.TrimSpace(argString(args, "session_mode"))
	} else {
		// Infer the mode from what was named, so a caller can move scope by
		// naming just a session_id or just a project_id — the same rule
		// create uses.
		switch {
		case hasSession && next.SessionID != "":
			next.Mode = entity.ScheduledSessionExisting
		case hasTemplate && strings.TrimSpace(argString(args, "session_template")) != "":
			next.Mode = entity.ScheduledSessionTemplate
		case hasProject && next.ProjectID != "" && !m.IsProjectScoped():
			next.Mode = entity.ScheduledSessionNew
		}
	}
	if hasTemplate {
		next.Template = strings.TrimSpace(argString(args, "session_template"))
	}
	if next.Mode == entity.ScheduledSessionExisting {
		// A session-scoped row carries no project/template config.
		next.ProjectID, next.Template = "", ""
	} else {
		next.SessionID = "" // the target is resolved per fire
	}
	if err := schedule.ValidateTargetSpec(next, now); err != nil {
		return err
	}

	// Authorize against the NEW target and adopt its owner. This is the gate
	// that stops a reschedule from parking work inside a project (or session)
	// the caller can't reach.
	owner, err := scheduleAuthorizeTarget(r, layout, next, user)
	if err != nil {
		return err
	}

	patch.ProjectID = &next.ProjectID
	patch.SessionMode = &next.Mode
	patch.SessionTemplate = &next.Template
	patch.SessionID = &next.SessionID
	patch.OwnerUserID = &owner
	return nil
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

// scheduleScope returns (ownerID, allOwners) for a list query. It mirrors
// the create/cancel gate (canManageSession = CanSeeAllSessions || IsAdmin):
// the app super-user AND any admin — including the in-process wick
// provider's synthetic RoleAdmin principal — enumerate every owner's
// schedules; a regular user is scoped to their own. nil user (stdio /
// tests) is unscoped so local tooling sees everything.
//
// This deliberately lets admins list across owners on THIS transport.
// Earlier it scoped plain admins to their own id, which broke the
// create→list symmetry for the internal principal (it stamps each row's
// owner_user_id with the real session owner, so a self-scoped list never
// matched and returned []). The UI monitor still applies its own
// admin_see_all filtering separately.
func scheduleScope(user *entity.User) (string, bool) {
	// Admins (incl. the in-process wick provider's synthetic RoleAdmin
	// principal) see all owners here, matching the create/cancel gate
	// (canManageSession = CanSeeAllSessions || IsAdmin). Without IsAdmin
	// the internal user — RoleAdmin but not IsOwner — could create/cancel
	// but list returned [] because the query scoped to its own id, which
	// never matches a row stamped with the real session owner's id.
	if user == nil || user.CanSeeAllSessions() || user.IsAdmin() {
		return "", true
	}
	return user.ID, false
}

// scheduleAuthorizeTarget checks the caller may schedule into a target and
// returns the owner id to stamp on the row.
//
// Session scope reuses canManageSession and inherits the session's owner.
// Project scope goes through project.CanAccess (admin / owner / tag share /
// shared-untagged) and inherits the project's owner — falling back to the
// caller for an ownerless shared project, so the row is never orphaned and
// still shows up in its creator's list.
//
// Errors are phrased as "not found" so a caller can't probe for the
// existence of sessions or projects they may not see.
func scheduleAuthorizeTarget(r *http.Request, layout agentconfig.Layout, target schedule.TargetSpec, user *entity.User) (string, error) {
	if target.Mode == entity.ScheduledSessionExisting {
		sess, err := session.Load(layout, target.SessionID)
		if err != nil {
			return "", fmt.Errorf("load session: %w", err)
		}
		if !canManageSession(user, sess.Meta.UserID) {
			// Match the title tools: don't leak that the session exists.
			return "", fmt.Errorf("session not found: %s", target.SessionID)
		}
		return sess.Meta.UserID, nil
	}

	meta, err := project.Load(layout, target.ProjectID)
	if err != nil {
		return "", fmt.Errorf("project not found: %s", target.ProjectID)
	}
	if !project.CanAccess(meta.Meta, scheduleProjectAccess(r, user)) {
		return "", fmt.Errorf("project not found: %s", target.ProjectID)
	}
	owner := meta.Meta.OwnerUserID
	if owner == "" && user != nil {
		owner = user.ID
	}
	return owner, nil
}

// scheduleCanManage gates the by-id lifecycle actions. The owner match is the
// base rule for both scopes; a project-scoped row additionally admits anyone
// the project itself is shared with (tag grant, or an untagged shared
// project), so a teammate who can open the project can also pause the job
// running in it. Session-scoped rows keep the stricter owner/admin rule.
func scheduleCanManage(r *http.Request, layout agentconfig.Layout, m entity.ScheduledMessage, user *entity.User) bool {
	if canManageSession(user, m.OwnerUserID) {
		return true
	}
	if !m.IsProjectScoped() || m.ProjectID == "" {
		return false
	}
	p, err := project.Load(layout, m.ProjectID)
	if err != nil {
		return false
	}
	return project.CanAccess(p.Meta, scheduleProjectAccess(r, user))
}

// scheduleProjectAccess builds the project-visibility identity for the
// calling principal. A nil user (stdio / tests) is treated as admin, matching
// scheduleScope's unscoped behavior on those transports.
func scheduleProjectAccess(r *http.Request, user *entity.User) project.Access {
	if user == nil {
		return project.Access{IsAdmin: true}
	}
	return project.Access{
		UserID:  user.ID,
		TagIDs:  login.GetUserTagIDs(r.Context()),
		IsAdmin: user.IsAdmin() || user.CanSeeAllSessions(),
	}
}

// scheduleSourceSession records which session asked for the schedule. An
// agent scheduling a project job from its own conversation should still be
// traceable, so the caller may pass source_session_id explicitly; otherwise
// it defaults to the target session (empty for project scope).
func scheduleSourceSession(args map[string]any, target schedule.TargetSpec) string {
	if v := strings.TrimSpace(argString(args, "source_session_id")); v != "" {
		return v
	}
	return target.SessionID
}

func scheduleVM(m entity.ScheduledMessage) map[string]any {
	vm := map[string]any{
		"id":         m.ID,
		"session_id": m.SessionID,
		"created_by": m.CreatedBy,
		"kind":       m.Kind,
		// EffectiveStatus folds `paused` in, so a caller reading only status
		// never sees "active" for something that will not fire.
		"status":     m.EffectiveStatus(),
		"message":    m.Message,
		"run_count":  m.RunCount,
	}
	// next_run_at is present only when there IS a next fire. A finished or
	// mid-claim row has none — publishing its stored run_at would hand back
	// the claim park sentinel (~100 years out).
	if next := m.NextRunAt(); next != nil {
		vm["next_run_at"] = next.UTC().Format(time.RFC3339)
		// run_at is emitted only for a ONE-SHOT, where "the run time" and "the
		// next fire" are the same thing and the name reads correctly. On a
		// recurring schedule it was a pure mirror of next_run_at, and the name
		// invited reading it as the creation time — so it is dropped there
		// rather than kept as a misleading alias.
		if !m.IsRecurring() {
			vm["run_at"] = next.UTC().Format(time.RFC3339)
		}
	}
	// Target fields are omitted for a plain session-scoped row so existing
	// consumers see byte-identical output to before project scope existed.
	if m.IsProjectScoped() {
		vm["session_mode"] = m.Mode()
		vm["project_id"] = m.ProjectID
		if m.SessionTemplate != "" {
			vm["session_template"] = m.SessionTemplate
		}
	}
	if m.LastSessionID != "" {
		vm["last_session_id"] = m.LastSessionID
	}
	// Which session asked for this. For a project job that is the only link
	// back to the conversation that set it up, since it has no session_id.
	if m.SourceSessionID != "" {
		vm["source_session_id"] = m.SourceSessionID
	}
	// Manual runs are reported separately from run_count so "ran 3×" stays the
	// count of SCHEDULED fires (what max_runs caps).
	if m.ManualRuns > 0 {
		vm["manual_runs"] = m.ManualRuns
	}
	// An in-flight manual run is reported as its own fact. next_run_at keeps
	// pointing at the schedule's real next fire, so nothing here reads as
	// "run_now moved the schedule".
	if m.ManualFirePending() {
		vm["manual_fire_pending"] = true
	}
	if m.Cron != "" {
		// State the zone the cron was read in; see serverZoneLabel.
		vm["cron_timezone"] = schedule.ServerZoneLabel(m.RunAt)
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
