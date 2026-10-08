// Package schedule stores and delivers future message injections into
// agent sessions (see internal/planning/in-progress/scheduled-messages.md).
// The store is the DB-backed persistence for scheduled messages; the
// runner (runner.go) polls it and delivers due messages through the pool.
//
// The feature is deliberately NOT built on the workflow engine — it is a
// standalone "check back later / remind me" primitive. Delivery reuses the
// normal pool send path so a fired schedule behaves like any inbound
// message: it spawns an idle session or queues behind a busy one.
package schedule

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/entity"
)

// ErrNotFound is returned when a schedule id does not exist.
var ErrNotFound = errors.New("scheduled message not found")

// Delivery is fail-fast, not retried: a schedule that fails to deliver
// (send error, or a vanished target session) is marked "failed" and never
// re-fired. attempts is stamped on each claim for observability only — a
// nudge that can't be delivered shouldn't spin. (If a retry cap is ever
// wanted, MarkFailed would compare attempts before terminating.)

// Store is the DB persistence for scheduled messages.
type Store struct {
	db *gorm.DB
	// layout locates watch run history, so a delete can take it along.
	// Zero (tests, stdio) leaves the files alone.
	layout agentconfig.Layout
}

func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// SetLayout tells the store where agent data lives, so deleting a watch also
// removes its run history.
func (s *Store) SetLayout(layout agentconfig.Layout) *Store {
	s.layout = layout
	return s
}

// Layout is the layout set by SetLayout.
func (s *Store) Layout() agentconfig.Layout { return s.layout }

// Create persists a new pending schedule and returns the stored row.
func (s *Store) Create(ctx context.Context, m *entity.ScheduledMessage) (*entity.ScheduledMessage, error) {
	if err := s.db.WithContext(ctx).Create(m).Error; err != nil {
		return nil, err
	}
	changed()
	return m, nil
}

// Get loads one schedule by id.
func (s *Store) Get(ctx context.Context, id string) (*entity.ScheduledMessage, error) {
	var m entity.ScheduledMessage
	err := s.db.WithContext(ctx).First(&m, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// ListForOwner returns schedules an owner may see, live-and-soonest first
// (see listOrder). When sessionID is non-empty the list is scoped to that
// session. When allOwners is true (admin) the owner filter is skipped.
func (s *Store) ListForOwner(ctx context.Context, ownerUserID, sessionID string, allOwners bool) ([]entity.ScheduledMessage, error) {
	return s.ListFiltered(ctx, ownerUserID, SessionScope{ID: sessionID}, "", allOwners)
}

// SessionScope identifies the session a listing is being made from, plus the
// project that session belongs to. Both are needed because a session sees two
// different kinds of schedule: the ones aimed at it, and the project jobs of
// the project it lives in.
type SessionScope struct {
	ID string
	// ProjectID is the session's own project ("" when unbound). A project
	// job belongs to the PROJECT, not to whichever session happened to
	// create it, so every session in that project must see it.
	ProjectID string
}

// ListFiltered is ListForOwner plus an optional project filter.
//
// The session filter matches four ways, because a session relates to a
// schedule in four ways:
//
//   - session_id        — it is the fixed delivery target
//   - source_session_id — the schedule was created from it
//   - last_session_id   — the last fire landed in it
//   - project_id        — it is a project job of THIS session's project
//
// That last one is what makes project jobs cross-session: they are owned by
// the project, so switching to a sibling session in the same project still
// shows (and can manage) them. Without it a job would be visible only from
// the conversation that happened to create it, which contradicts the whole
// point of project scope.
func (s *Store) ListFiltered(ctx context.Context, ownerUserID string, scope SessionScope, projectID string, allOwners bool) ([]entity.ScheduledMessage, error) {
	return s.List(ctx, ListQuery{
		OwnerUserID: ownerUserID,
		Scope:       scope,
		ProjectID:   projectID,
		AllOwners:   allOwners,
	})
}

// ListQuery describes one listing. Zero values mean "no filter", except
// Limit (defaults to listDefaultLimit) — an unbounded default is how a
// caller accidentally pulls every schedule ever created.
type ListQuery struct {
	OwnerUserID string
	Scope       SessionScope
	ProjectID   string
	AllOwners   bool
	// Statuses restricts the result to these statuses. Empty means every
	// status; use LiveStatuses() for the "what's still going to happen" view
	// that callers usually want.
	//
	// "paused" is accepted here even though it is never stored: it selects
	// live rows with paused=true, matching the status the API reports.
	Statuses []string
	// Paused filters on the pause flag: nil = either, true/false = only that.
	// Set indirectly by asking for status "paused" / excluding it.
	Paused *bool
	// TargetSessionID is the STRICT session filter: only schedules that
	// deliver into this session. Scope (above) is the broad "related to this
	// session" match, which also catches project jobs created from it — a
	// useful default, but it cannot answer "what will land HERE?".
	TargetSessionID string
	// Since, when non-zero, drops rows not updated since then — for trimming
	// long-finished history out of a listing.
	Since time.Time
	Limit int
}

// listDefaultLimit caps a listing that didn't ask for a size. Generous for a
// UI page, small enough that an unfiltered call can't return an unbounded
// history.
const listDefaultLimit = 500

// LiveStatuses returns the statuses a schedule that can still fire is in —
// the sensible default filter for "show me my schedules".
func LiveStatuses() []string {
	return append([]string(nil), liveStatuses...)
}

// List runs a filtered listing; see listOrder for the sort.
func (s *Store) List(ctx context.Context, q ListQuery) ([]entity.ScheduledMessage, error) {
	tx := s.db.WithContext(ctx).Model(&entity.ScheduledMessage{})
	if !q.AllOwners {
		tx = tx.Where("owner_user_id = ?", q.OwnerUserID)
	}
	if q.Scope.ID != "" {
		cond := "session_id = ? OR source_session_id = ? OR last_session_id = ?"
		args := []any{q.Scope.ID, q.Scope.ID, q.Scope.ID}
		if q.Scope.ProjectID != "" {
			// project_id is only ever set on project-scoped rows, so this
			// can't pull in a session nudge belonging to a sibling session.
			cond += " OR project_id = ?"
			args = append(args, q.Scope.ProjectID)
		}
		tx = tx.Where(cond, args...)
	}
	if q.TargetSessionID != "" {
		tx = tx.Where("session_id = ?", q.TargetSessionID)
	}
	if q.ProjectID != "" {
		tx = tx.Where("project_id = ?", q.ProjectID)
	}
	if len(q.Statuses) > 0 {
		tx = tx.Where("status IN ?", q.Statuses)
	}
	if q.Paused != nil {
		tx = tx.Where("paused = ?", *q.Paused)
	}
	if !q.Since.IsZero() {
		tx = tx.Where("updated_at >= ?", q.Since)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = listDefaultLimit
	}
	var out []entity.ScheduledMessage
	if err := tx.Order(listOrder).Limit(limit).Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// listOrder sorts a listing the way the question is actually asked.
//
// Live schedules come first, soonest fire first — "what is scheduled?" means
// "what happens next", and putting them first means the LIMIT truncates old
// history rather than the next fire. Terminal rows follow, most recent first.
//
// A single `run_at DESC` couldn't express this: a live row's run_at is in the
// future and a terminal row's is in the past, so one direction is always wrong
// for half the set — and the cap then cut the imminent fires.
// Three tiers, because "relevant" means something different per status:
//
//	0  live            — soonest fire first (what happens next)
//	1  terminal, fired — most recent fire first (recent history)
//	2  terminal, never fired — last; a cancelled-before-firing row is the least
//	                       interesting thing in the list
//
// Tier 2 exists because Cancel keeps run_at when there is no last_run_at to
// fall back to (a row cancelled before its first fire), leaving a FUTURE
// timestamp on a dead row. Sorting on that put "cancelled, never ran" above
// schedules that had just fired — and with a small limit, pushed the real
// history out entirely. Tiering on last_run_at IS NULL fixes it at the sort
// key rather than by rewriting stored timestamps.
const listOrder = "CASE WHEN status IN ('pending','active') THEN 0 " +
	"WHEN last_run_at IS NOT NULL THEN 1 ELSE 2 END ASC, " +
	"CASE WHEN status IN ('pending','active') THEN run_at END ASC, " +
	"last_run_at DESC, " +
	"created_at DESC"

// liveStatuses are the statuses a schedule can be claimed/cancelled/paused
// from: a one-shot waiting to fire, or a recurring one still running.
var liveStatuses = []string{entity.ScheduledStatusPending, entity.ScheduledStatusActive}

// ListAll returns every schedule, live-and-soonest first (see listOrder),
// capped. Used by the global
// cross-session monitor, which then filters to the sessions the caller may
// access. No owner/session scoping here — access is enforced by the caller.
func (s *Store) ListAll(ctx context.Context, limit int) ([]entity.ScheduledMessage, error) {
	if limit <= 0 {
		limit = 2000
	}
	var out []entity.ScheduledMessage
	if err := s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).
		Order(listOrder).Limit(limit).Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// Cancel stops a live schedule (pending one-shot or active recurring). A
// finished/failed/already-cancelled row returns ErrNotFound so the caller
// can't tell a done schedule from a missing one.
func (s *Store) Cancel(ctx context.Context, id string) error {
	res := s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).
		Where("id = ? AND status IN ?", id, liveStatuses).
		Updates(map[string]any{
			"status": entity.ScheduledStatusCancelled,
			// Unpark, in case this cancels a row mid-claim: a terminal row
			// must never carry the far-future claim sentinel.
			"run_at":     gorm.Expr("COALESCE(last_run_at, run_at)"),
			"updated_at": time.Now(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	changed()
	return nil
}

// CountTargeting counts the live schedules CancelTargeting would cancel,
// for a dialog that names what is connected to a project.
func (s *Store) CountTargeting(ctx context.Context, projectID string, sessionIDs []string) (int64, error) {
	cond, args := targetingCond(projectID, sessionIDs)
	if cond == "" {
		return 0, nil
	}
	var n int64
	err := s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).
		Where("status IN ?", liveStatuses).
		Where(cond, args...).
		Count(&n).Error
	return n, err
}

// targetingCond is the WHERE of "fires into the project or one of the
// sessions"; "" when both are empty.
func targetingCond(projectID string, sessionIDs []string) (string, []any) {
	cond, args := "", []any{}
	if projectID != "" {
		cond = "project_id = ?"
		args = append(args, projectID)
	}
	if len(sessionIDs) > 0 {
		if cond != "" {
			cond += " OR "
		}
		cond += "session_id IN ?"
		args = append(args, sessionIDs)
	}
	return cond, args
}

// CancelTargeting cancels every live schedule that would fire into the
// project or into one of the given sessions — what a project delete must
// do so nothing fires into a conversation that no longer exists. A row that
// was only REQUESTED from one of those sessions (SourceSessionID) but
// targets elsewhere is left alone: its target is still there. Returns how
// many rows were cancelled.
func (s *Store) CancelTargeting(ctx context.Context, projectID string, sessionIDs []string) (int64, error) {
	cond, args := targetingCond(projectID, sessionIDs)
	if cond == "" {
		return 0, nil
	}
	res := s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).
		Where("status IN ?", liveStatuses).
		Where(cond, args...).
		Updates(map[string]any{
			"status":     entity.ScheduledStatusCancelled,
			"run_at":     gorm.Expr("COALESCE(last_run_at, run_at)"),
			"updated_at": time.Now(),
		})
	changed()
	return res.RowsAffected, res.Error
}

// SetPaused pauses or resumes a recurring schedule. On resume the caller
// supplies the recomputed next run_at (the runner/handler figures out the
// next fire from now). Only recurring, non-terminal rows can be toggled.
func (s *Store) SetPaused(ctx context.Context, id string, paused bool, nextRunAt time.Time) error {
	// A pause or resume by hand is the user's call from now on: the row
	// no longer follows its agent's disable/enable.
	updates := map[string]any{"paused": paused, "held_by_agent": false, "updated_at": time.Now()}
	if !paused {
		updates["run_at"] = nextRunAt
	}
	res := s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).
		Where("id = ? AND kind = ? AND status = ?", id, entity.ScheduledKindRecurring, entity.ScheduledStatusActive).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	changed()
	return nil
}

// Reschedule edits a live schedule's timing and/or message. Any zero-valued
// field is left unchanged. Used by both the UI edit and the MCP reschedule
// action. Terminal rows return ErrNotFound.
func (s *Store) Reschedule(ctx context.Context, id string, patch SchedulePatch) error {
	updates := map[string]any{"updated_at": time.Now()}
	if !patch.RunAt.IsZero() {
		updates["run_at"] = patch.RunAt
	}
	// Interval and cron are mutually exclusive: setting one clears the
	// other. A patch may carry both with one empty (a parsed spec does), so
	// only a non-empty value clears its sibling — else the empty one would
	// wipe the value just set.
	if patch.IntervalMs != nil {
		updates["interval_ms"] = *patch.IntervalMs
		if *patch.IntervalMs > 0 {
			updates["cron"] = ""
		}
	}
	if patch.Cron != nil {
		updates["cron"] = *patch.Cron
		if *patch.Cron != "" {
			updates["interval_ms"] = int64(0)
		}
	}
	if patch.Message != nil {
		updates["message"] = *patch.Message
	}
	if patch.MaxRuns != nil {
		updates["max_runs"] = *patch.MaxRuns
	}
	if patch.EndsAt != nil {
		updates["ends_at"] = patch.EndsAt
	}
	// Target edits. A schedule may be re-pointed within its scope (change
	// the project, switch new↔template, fix a pattern) — crossing between
	// session- and project-scoped is rejected upstream, not here.
	if patch.ProjectID != nil {
		updates["project_id"] = *patch.ProjectID
	}
	if patch.SessionMode != nil {
		updates["session_mode"] = *patch.SessionMode
	}
	if patch.SessionTemplate != nil {
		updates["session_template"] = *patch.SessionTemplate
	}
	if patch.SessionID != nil {
		updates["session_id"] = *patch.SessionID
	}
	if patch.RunAsUserID != nil {
		updates["run_as_user_id"] = *patch.RunAsUserID
	}
	if patch.OwnerUserID != nil {
		updates["owner_user_id"] = *patch.OwnerUserID
	}
	res := s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).
		Where("id = ? AND status IN ?", id, liveStatuses).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	changed()
	return nil
}

// SchedulePatch carries an edit to a live schedule. Nil pointers mean "leave
// as-is"; a zero RunAt means "don't change the next fire time".
type SchedulePatch struct {
	RunAt      time.Time
	IntervalMs *int64
	Cron       *string
	Message    *string
	MaxRuns    *int
	EndsAt     *time.Time
	// Target edits — where the next fire lands. Nil means "leave as-is";
	// an empty-string pointer clears the column.
	ProjectID       *string
	SessionMode     *string
	SessionTemplate *string
	// SessionID moves a schedule's fixed target, and is what a scope move
	// sets or clears (a project-scoped row carries no session id).
	SessionID *string
	// OwnerUserID re-stamps ownership. A scope move changes what the row
	// belongs to — a session's owner vs a project's — so the owner has to
	// follow, or the row becomes invisible to its own lists.
	OwnerUserID *string
	// RunAsUserID re-points the identity a fire runs as. A pointer to the
	// empty string CLEARS the override, handing the schedule back to its
	// owner — which is why it is a pointer and not a plain string: "not
	// mentioned" and "set back to default" have to be different requests.
	RunAsUserID *string
}

// RunNow makes a live schedule due immediately, so the next runner tick
// claims and delivers it through the ordinary path. Nothing about the
// schedule's definition changes: a recurring row still advances from the
// moment it fires, so a manual run shifts the following fire exactly as a
// natural one would.
//
// Implemented as "move run_at to now" rather than a direct deliver call, so a
// manual run reuses the same atomic claim as every other fire — it can't
// double-fire against a concurrent tick, and it needs no second copy of the
// delivery logic. A paused schedule is un-paused by the same call, since
// asking to run it now plainly means "yes, run it".
func (s *Store) RunNow(ctx context.Context, id string) error {
	now := time.Now()
	// next_run_at is remembered in `anchor` only for cron/one-shot rows that
	// have none; for interval rows the anchor already holds the series origin.
	// The manual_fire flag is what makes this an EXTRA fire rather than the
	// next scheduled one: the runner reads it and skips both the count and the
	// cadence advance, so a manual run cannot consume max_runs or shift the
	// series. `pending_run_at` parks the real next fire while the manual one
	// is in flight.
	var m entity.ScheduledMessage
	if err := s.db.WithContext(ctx).First(&m, "id = ? AND status IN ?", id, liveStatuses).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrNotFound
		}
		return err
	}
	updates := map[string]any{
		"run_at":         now,
		"pending_run_at": m.RunAt, // restored after the manual fire
		"manual_fire":    true,
		"paused":         false,
		"updated_at":     now,
	}
	res := s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).
		Where("id = ? AND status IN ?", id, liveStatuses).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	changed()
	return nil
}

// ClaimDue atomically claims up to `limit` live, non-paused rows whose run_at
// has passed. The claim uses the OBSERVED run_at as an optimistic-lock guard:
// the UPDATE only lands if run_at is still what we read, so a concurrent tick
// (or a second wick instance sharing the DB) can't double-fire the same
// occurrence. Claiming stamps last_run_at + run_count and pushes run_at out to
// a sentinel far future, parking the row until the runner sets its real next
// state (advance for recurring, done for one-shot) after delivery.
//
// Returned rows carry the pre-claim values plus RunCount already incremented,
// so the runner can compute the next fire with advance(row, firedAt, row.RunCount).
//
// ClaimDue takes message schedules only. Watches never go through a claim:
// they are scheduled in memory by the runner (watch_runner.go) under a DB
// lease, so a pending tick costs no query at all.
func (s *Store) ClaimDue(ctx context.Context, now time.Time, limit int) ([]entity.ScheduledMessage, error) {
	return s.claimDue(ctx, now, limit, "type <> ?", entity.ScheduledTypeWatch)
}

func (s *Store) claimDue(ctx context.Context, now time.Time, limit int, typeCond string, typeArg any) ([]entity.ScheduledMessage, error) {
	var candidates []entity.ScheduledMessage
	err := s.db.WithContext(ctx).
		Where("status IN ? AND paused = ? AND run_at <= ?", liveStatuses, false, now).
		Where(typeCond, typeArg).
		Order("run_at ASC").Limit(limit).Find(&candidates).Error
	if err != nil {
		return nil, err
	}
	// Park claimed rows far in the future until the runner sets their real
	// next state (Finalize for success, MarkFailed on error). The park must
	// be well beyond any real poll window: if the process crashes AFTER the
	// claim but BEFORE finalize, the row must NOT re-enter the due set on
	// restart (run_at <= now) and re-fire. A ~100-year park makes reclaim
	// impossible in practice; worst case a crash in that narrow window drops
	// one fire (at-most-once), which is the right bias for a nudge — better a
	// missed reminder than a duplicate spam.
	parked := now.AddDate(100, 0, 0)
	claimed := make([]entity.ScheduledMessage, 0, len(candidates))
	for _, c := range candidates {
		// A manual run is an EXTRA fire: it stamps last_run_at (it did
		// happen) and manual_runs, but must not touch run_count, or a
		// max_runs-capped schedule would lose budget to a test.
		upd := map[string]any{
			"run_at":      parked,
			"last_run_at": now,
			"attempts":    gorm.Expr("attempts + 1"),
			"updated_at":  now,
		}
		if c.ManualFire {
			upd["manual_runs"] = gorm.Expr("manual_runs + 1")
		} else {
			upd["run_count"] = gorm.Expr("run_count + 1")
		}
		res := s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).
			Where("id = ? AND run_at = ? AND status IN ? AND paused = ?", c.ID, c.RunAt, liveStatuses, false).
			Updates(upd)
		if res.Error != nil {
			return claimed, res.Error
		}
		if res.RowsAffected == 1 {
			c.LastRunAt = &now
			c.Attempts++
			if c.ManualFire {
				c.ManualRuns++
			} else {
				c.RunCount++
			}
			claimed = append(claimed, c)
		}
	}
	return claimed, nil
}

// Finalize sets a claimed row's terminal or next state after delivery:
//   - one-shot success           → done
//   - recurring, has a next fire → active with run_at = next
//   - recurring, stop condition  → done
//
// nextRunAt.IsZero() means "no next fire" (finish). lastSessionID records
// where this fire actually landed (empty leaves the stored value alone).
// Called only for a successful delivery.
//
// `kind` is the schedule's own kind, used to pick the live status when a next
// fire remains: `active` for recurring, `pending` for a one-shot (which keeps
// a pending fire after a manual run).
func (s *Store) Finalize(ctx context.Context, id, kind string, nextRunAt time.Time, lastSessionID string) error {
	hasNextFire := !nextRunAt.IsZero()
	liveStatus := entity.ScheduledStatusPending
	if kind == entity.ScheduledKindRecurring {
		liveStatus = entity.ScheduledStatusActive
	}
	updates := map[string]any{
		"updated_at": time.Now(),
		"attempts":   0,
		"last_error": "",
		// The manual claim is over either way; clearing both here means a
		// crashed manual fire can't leave the row permanently marked.
		"manual_fire":    false,
		"pending_run_at": nil,
	}
	if lastSessionID != "" {
		updates["last_session_id"] = lastSessionID
	}
	if hasNextFire && !nextRunAt.IsZero() {
		// Stay live, in the status this KIND is live in: `active` for a
		// recurring schedule, `pending` for a one-shot — which can reach this
		// branch after a manual run put its own pending fire back.
		updates["status"] = liveStatus
		updates["run_at"] = nextRunAt
	} else {
		updates["status"] = entity.ScheduledStatusDone
		// Unpark: a finished row keeps the time it actually fired, not the
		// claim's far-future sentinel. Leaving the sentinel in place made a
		// done schedule report "next run in 100 years" to the API and sort
		// to the top of a run_at ordering.
		updates["run_at"] = gorm.Expr("COALESCE(last_run_at, run_at)")
	}
	return s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).
		Where("id = ?", id).Updates(updates).Error
}

// MarkFailed records a delivery error and stops the schedule. A failing send
// (or a vanished session) terminates the row rather than retrying forever —
// for recurring this also auto-cancels, matching the "session gone → error +
// cancel" rule. Like Finalize, it unparks run_at so a failed row doesn't
// advertise a fire time a century out.
func (s *Store) MarkFailed(ctx context.Context, id, reason string) error {
	return s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     entity.ScheduledStatusFailed,
			"last_error": reason,
			"run_at":     gorm.Expr("COALESCE(last_run_at, run_at)"),
			"updated_at": time.Now(),
		}).Error
}

// WatchOutcome is the state a watch run leaves its row in. The runner keeps
// the live counters in memory and writes them here only when something worth
// persisting happened (see watch_runner.go).
type WatchOutcome struct {
	Result            string
	ConsecutiveErrors int
	LastError         string
	RunCount          int
	ManualRuns        int
	LastRunAt         *time.Time
	// Status, when set, ends the schedule (done / failed). Empty keeps it
	// live with run_at = Next.
	Status        string
	Next          time.Time
	LastSessionID string
	// At is the updated_at stamped on the row, so the runner can tell its
	// own write from somebody else's edit on the next refresh.
	At time.Time
}

// SaveWatchOutcome writes a watch's state after a run. It also clears a
// finished manual run (manual_fire / pending_run_at).
func (s *Store) SaveWatchOutcome(ctx context.Context, id, kind string, o WatchOutcome) error {
	updates := map[string]any{
		"updated_at":         o.At,
		"attempts":           0,
		"last_result":        o.Result,
		"consecutive_errors": o.ConsecutiveErrors,
		"last_error":         o.LastError,
		"run_count":          o.RunCount,
		"manual_runs":        o.ManualRuns,
		"manual_fire":        false,
		"pending_run_at":     nil,
	}
	if o.LastRunAt != nil {
		updates["last_run_at"] = *o.LastRunAt
	}
	if o.LastSessionID != "" {
		updates["last_session_id"] = o.LastSessionID
	}
	switch {
	case o.Status != "":
		updates["status"] = o.Status
		updates["run_at"] = gorm.Expr("COALESCE(last_run_at, run_at)")
	case o.Next.IsZero():
		updates["status"] = entity.ScheduledStatusDone
		updates["run_at"] = gorm.Expr("COALESCE(last_run_at, run_at)")
	default:
		live := entity.ScheduledStatusPending
		if kind == entity.ScheduledKindRecurring {
			live = entity.ScheduledStatusActive
		}
		updates["status"] = live
		updates["run_at"] = o.Next
	}
	res := s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).
		Where("id = ? AND status IN ?", id, liveStatuses).Updates(updates)
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNotFound // cancelled/finished meanwhile; never resurrect it
	}
	return res.Error
}

// FlushWatchProgress persists a live watch's counters without touching its
// result — the runner's periodic (≤ 1/min) checkpoint for pending runs.
func (s *Store) FlushWatchProgress(ctx context.Context, id string, runCount int, lastRunAt *time.Time, next, at time.Time) error {
	updates := map[string]any{"run_count": runCount, "updated_at": at}
	if lastRunAt != nil {
		updates["last_run_at"] = *lastRunAt
	}
	if !next.IsZero() {
		updates["run_at"] = next
	}
	return s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).
		Where("id = ? AND status IN ? AND manual_fire = ?", id, liveStatuses, false).Updates(updates).Error
}

// LoadLiveWatches is the runner's one query per refresh: every live watch.
func (s *Store) LoadLiveWatches(ctx context.Context) ([]entity.ScheduledMessage, error) {
	var out []entity.ScheduledMessage
	err := s.db.WithContext(ctx).
		Where("type = ? AND status IN ?", entity.ScheduledTypeWatch, liveStatuses).
		Find(&out).Error
	return out, err
}

// CountLiveWatches counts an owner's live watches, for the per-user cap.
func (s *Store) CountLiveWatches(ctx context.Context, ownerUserID string) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).
		Where("type = ? AND status IN ? AND owner_user_id = ?", entity.ScheduledTypeWatch, liveStatuses, ownerUserID).
		Count(&n).Error
	return n, err
}

// CheckWatchCap refuses one more live watch for ownerUserID once they hold
// MaxLiveWatchesPerUser. Fail-closed: a count that cannot be read refuses
// too. Admins are not capped (the caller decides).
func (s *Store) CheckWatchCap(ctx context.Context, ownerUserID string) error {
	n, err := s.CountLiveWatches(ctx, ownerUserID)
	if err != nil {
		return fmt.Errorf("watch limit could not be checked (%v) — try again", err)
	}
	if n >= MaxLiveWatchesPerUser {
		return fmt.Errorf("watch limit reached: %d live watches per user — cancel one first", MaxLiveWatchesPerUser)
	}
	return nil
}

// AcquireLease takes or renews the named lease for holder until now+ttl.
// It succeeds when the lease is free, expired, or already holder's — so only
// one wick process (across a reload handover, or two instances on one DB)
// runs the watches at a time.
func (s *Store) AcquireLease(ctx context.Context, name, holder string, ttl time.Duration) (bool, error) {
	now := time.Now()
	res := s.db.WithContext(ctx).Model(&entity.ScheduleLease{}).
		Where("name = ? AND (holder = ? OR expires_at < ?)", name, holder, now).
		Updates(map[string]any{"holder": holder, "expires_at": now.Add(ttl)})
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 1 {
		return true, nil
	}
	res = s.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).
		Create(&entity.ScheduleLease{Name: name, Holder: holder, ExpiresAt: now.Add(ttl)})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// ReleaseLease lets the lease go at once (shutdown), so the next process does
// not wait out the TTL.
func (s *Store) ReleaseLease(ctx context.Context, name, holder string) error {
	return s.db.WithContext(ctx).Model(&entity.ScheduleLease{}).
		Where("name = ? AND holder = ?", name, holder).
		Update("expires_at", time.Now().Add(-time.Second)).Error
}

// SetSteps replaces a live watch's steps.
func (s *Store) SetSteps(ctx context.Context, id, steps string) error {
	res := s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).
		Where("id = ? AND type = ? AND status IN ?", id, entity.ScheduledTypeWatch, liveStatuses).
		Updates(map[string]any{"steps": steps, "steps_rev": gorm.Expr("steps_rev + 1"), "updated_at": time.Now()})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	changed()
	return nil
}

// changed tells the in-process runner a schedule was edited, so a watch it
// holds in memory picks up a pause / cancel / reschedule / run-now now
// instead of at the next refresh. A no-op without a running runner.
func changed() { WakeRunner() }

// ScheduleIDsToKeep is the history sweep's one query: every live schedule,
// plus finished ones touched since cutoff. A schedules/<id>/ folder whose id
// is not in the set is an orphan or long finished, and is removed.
func (s *Store) ScheduleIDsToKeep(ctx context.Context, cutoff time.Time) (map[string]bool, error) {
	var ids []string
	if err := s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).
		Where("status IN ? OR updated_at >= ?", liveStatuses, cutoff).
		Pluck("id", &ids).Error; err != nil {
		return nil, err
	}
	keep := make(map[string]bool, len(ids))
	for _, id := range ids {
		keep[id] = true
	}
	return keep, nil
}

// Reactivate brings a finished (done) or failed watch back to life: active,
// unpaused, a fresh error streak and run count, first run at next and a new
// time limit endsAt (see WatchResumeEndsAt). Message
// schedules are not reactivated — their done is final.
func (s *Store) Reactivate(ctx context.Context, id string, next time.Time, endsAt *time.Time) error {
	var ends any // nil endsAt → NULL: no time limit
	if endsAt != nil {
		ends = *endsAt
	}
	res := s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).
		Where("id = ? AND type = ? AND kind = ? AND status IN ?", id, entity.ScheduledTypeWatch, entity.ScheduledKindRecurring,
			[]string{entity.ScheduledStatusDone, entity.ScheduledStatusFailed}).
		Updates(map[string]any{
			"status": entity.ScheduledStatusActive, "paused": false, "held_by_agent": false,
			"consecutive_errors": 0, "last_error": "", "last_result": "", "run_count": 0,
			"manual_fire": false, "pending_run_at": nil, "run_at": next, "ends_at": ends, "updated_at": time.Now(),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	changed()
	return nil
}
