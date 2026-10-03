package schedule

import (
	"context"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

// The functions here back a Team agent's "Scheduled" drawer. An agent owns
// no column on a schedule; a schedule belongs to an agent by WHERE it
// fires — into the agent's project, or into one of the agent's sessions.
// That is the same "targeting" relation a project delete uses
// (targetingCond), so a schedule the agent made for itself through
// wick_schedule_message shows up in the drawer with no extra tagging.

// ListTargeting lists the schedules that fire into the project or into one
// of the sessions: every live row, plus terminal rows touched since
// `since` (zero = all history). Sorted like every other listing.
func (s *Store) ListTargeting(ctx context.Context, projectID string, sessionIDs []string, since time.Time) ([]entity.ScheduledMessage, error) {
	cond, args := targetingCond(projectID, sessionIDs)
	if cond == "" {
		return nil, nil
	}
	tx := s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).Where(cond, args...)
	if !since.IsZero() {
		tx = tx.Where("status IN ? OR updated_at >= ?", liveStatuses, since)
	}
	var out []entity.ScheduledMessage
	if err := tx.Order(listOrder).Limit(listDefaultLimit).Find(&out).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// HoldTargeting pauses every live, running schedule aimed at the project
// or sessions and marks the pause as wick's (held_by_agent), for an agent
// being disabled. Rows the user already paused are left alone and keep no
// mark, so ReleaseTargeting will not wake them.
func (s *Store) HoldTargeting(ctx context.Context, projectID string, sessionIDs []string) (int64, error) {
	cond, args := targetingCond(projectID, sessionIDs)
	if cond == "" {
		return 0, nil
	}
	res := s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).
		Where("status IN ? AND paused = ?", liveStatuses, false).
		Where(cond, args...).
		Updates(map[string]any{"paused": true, "held_by_agent": true, "updated_at": time.Now()})
	return res.RowsAffected, res.Error
}

// ReleaseTargeting resumes the rows HoldTargeting paused, for an agent
// being enabled again. A recurring row's next fire is recomputed from now
// (see NextFrom), so the disabled stretch is skipped rather than replayed;
// a one-shot keeps its time and fires on the next tick if that has passed.
func (s *Store) ReleaseTargeting(ctx context.Context, projectID string, sessionIDs []string, now time.Time) (int64, error) {
	cond, args := targetingCond(projectID, sessionIDs)
	if cond == "" {
		return 0, nil
	}
	var held []entity.ScheduledMessage
	if err := s.db.WithContext(ctx).
		Where("status IN ? AND held_by_agent = ?", liveStatuses, true).
		Where(cond, args...).Find(&held).Error; err != nil {
		return 0, err
	}
	var n int64
	for _, m := range held {
		upd := map[string]any{"paused": false, "held_by_agent": false, "updated_at": now}
		if m.IsRecurring() {
			if next, err := NextFrom(m, now); err == nil {
				upd["run_at"] = next
			}
		}
		res := s.db.WithContext(ctx).Model(&entity.ScheduledMessage{}).
			Where("id = ? AND held_by_agent = ?", m.ID, true).Updates(upd)
		if res.Error != nil {
			return n, res.Error
		}
		n += res.RowsAffected
	}
	return n, nil
}

// Delete removes a schedule row outright — the drawer's Delete, as opposed
// to Cancel, which keeps a terminal row as history.
func (s *Store) Delete(ctx context.Context, id string) error {
	res := s.db.WithContext(ctx).Where("id = ?", id).Delete(&entity.ScheduledMessage{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteTargeting removes every schedule (any status) aimed at the project
// or sessions — for an agent being deleted, whose drawer goes with it.
func (s *Store) DeleteTargeting(ctx context.Context, projectID string, sessionIDs []string) (int64, error) {
	cond, args := targetingCond(projectID, sessionIDs)
	if cond == "" {
		return 0, nil
	}
	res := s.db.WithContext(ctx).Where(cond, args...).Delete(&entity.ScheduledMessage{})
	return res.RowsAffected, res.Error
}
