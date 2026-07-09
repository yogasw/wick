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

// ListForOwner returns schedules an owner may see, newest run_at first.
// When sessionID is non-empty the list is scoped to that session. When
// allOwners is true (admin) the owner filter is skipped.
func (s *Store) ListForOwner(ctx context.Context, ownerUserID, sessionID string, allOwners bool) ([]entity.ScheduledMessage, error) {
	q := s.db.WithContext(ctx).Model(&entity.ScheduledMessage{})
	if !allOwners {
		q = q.Where("owner_user_id = ?", ownerUserID)
	}
	if sessionID != "" {
		q = q.Where("session_id = ?", sessionID)
	}
	var out []entity.ScheduledMessage
	if err := q.Order("run_at DESC").Limit(500).Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// liveStatuses are the statuses a schedule can be claimed/cancelled/paused
// from: a one-shot waiting to fire, or a recurring one still running.
var liveStatuses = []string{entity.ScheduledStatusPending, entity.ScheduledStatusActive}

// ListAll returns every schedule newest-first, capped. Used by the global
// cross-session monitor, which then filters to the sessions the caller may
// access. No owner/session scoping here — access is enforced by the caller.
func (s *Store) ListAll(ctx context.Context, limit int) ([]entity.ScheduledMessage, error) {
	if limit <= 0 {
		limit = 2000
	}
	var out []entity.ScheduledMessage
	if err := s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).
		Order("run_at DESC").Limit(limit).Find(&out).Error; err != nil {
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
		Updates(map[string]any{"status": entity.ScheduledStatusCancelled, "updated_at": time.Now()})
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
	// Park claimed rows here until the runner sets the real next state. Chosen
	// so a crash between claim and finalize just delays the row ~maxCronScan,
	// not double-fire.
	parked := now.Add(2 * time.Hour)
	claimed := make([]entity.ScheduledMessage, 0, len(candidates))
	for _, c := range candidates {
		res := s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).
			Where("id = ? AND run_at = ? AND status IN ? AND paused = ?", c.ID, c.RunAt, liveStatuses, false).
			Updates(map[string]any{
				"run_at":      parked,
				"last_run_at": now,
				"run_count":   gorm.Expr("run_count + 1"),
				"attempts":    gorm.Expr("attempts + 1"),
				"updated_at":  now,
			})
		if res.Error != nil {
			return claimed, res.Error
		}
		if res.RowsAffected == 1 {
			c.LastRunAt = &now
			c.RunCount++
			c.Attempts++
			claimed = append(claimed, c)
		}
	}
	return claimed, nil
}

// Finalize sets a claimed row's terminal or next state after delivery:
//   - one-shot success           → done
//   - recurring, has a next fire → active with run_at = next
//   - recurring, stop condition  → done
// nextRunAt.IsZero() means "no next fire" (finish). Called only for a
// successful delivery.
func (s *Store) Finalize(ctx context.Context, id string, recurring bool, nextRunAt time.Time) error {
	updates := map[string]any{"updated_at": time.Now(), "attempts": 0, "last_error": ""}
	if recurring && !nextRunAt.IsZero() {
		updates["status"] = entity.ScheduledStatusActive
		updates["run_at"] = nextRunAt
	} else {
		updates["status"] = entity.ScheduledStatusDone
	}
	return s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).
		Where("id = ?", id).Updates(updates).Error
}

// MarkFailed records a delivery error and stops the schedule. A failing send
// (or a vanished session) terminates the row rather than retrying forever —
// for recurring this also auto-cancels, matching the "session gone → error +
// cancel" rule.
func (s *Store) MarkFailed(ctx context.Context, id, reason string) error {
	return s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"status":     entity.ScheduledStatusFailed,
			"last_error": reason,
			"updated_at": time.Now(),
		}).Error
}
