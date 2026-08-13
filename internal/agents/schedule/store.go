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
	"time"

	"gorm.io/gorm"

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
}

func NewStore(db *gorm.DB) *Store { return &Store{db: db} }

// Create persists a new pending schedule and returns the stored row.
func (s *Store) Create(ctx context.Context, m *entity.ScheduledMessage) (*entity.ScheduledMessage, error) {
	if err := s.db.WithContext(ctx).Create(m).Error; err != nil {
		return nil, err
	}
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
	return nil
}

// SetPaused pauses or resumes a recurring schedule. On resume the caller
// supplies the recomputed next run_at (the runner/handler figures out the
// next fire from now). Only recurring, non-terminal rows can be toggled.
func (s *Store) SetPaused(ctx context.Context, id string, paused bool, nextRunAt time.Time) error {
	updates := map[string]any{"paused": paused, "updated_at": time.Now()}
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
	if patch.IntervalMs != nil {
		updates["interval_ms"] = *patch.IntervalMs
		updates["cron"] = "" // interval and cron are mutually exclusive
	}
	if patch.Cron != nil {
		updates["cron"] = *patch.Cron
		updates["interval_ms"] = int64(0)
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
func (s *Store) ClaimDue(ctx context.Context, now time.Time, limit int) ([]entity.ScheduledMessage, error) {
	var candidates []entity.ScheduledMessage
	err := s.db.WithContext(ctx).
		Where("status IN ? AND paused = ? AND run_at <= ?", liveStatuses, false, now).
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
