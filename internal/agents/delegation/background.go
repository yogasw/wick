package delegation

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/entity"
)

// Background work reporting.
//
// A leader that fires a sub-agent in the background ends its turn right after,
// so a channel that only follows the leader's own turn goes quiet while the
// work carries on. The web UI does not have this problem: its sub-agent rail
// reads the delegation rows directly and spins while any is queued or running.
// These hooks hand a channel that same picture, from the same rows, so both
// surfaces agree on whether anything is still working.

// startTaskRunes caps the task excerpt a start notice carries. A notice is a
// one-line heads-up, not a copy of the brief.
const startTaskRunes = 80

// BackgroundStart describes one background delegation that was just accepted.
type BackgroundStart struct {
	Agent Survivor
	// Task is the brief the sub-agent was given, clipped to startTaskRunes.
	Task string
	// Queued is true when it is waiting for a slot rather than running.
	Queued bool
}

// ActiveBackground returns the background sub-agents directly under
// parentSessionID that are still queued or running, oldest first.
//
// Same rows and the same live test as the web UI's sub-agent rail
// (ListByParent + queued|running), so a channel banner and the rail spinner
// are answering one question from one source rather than keeping two
// trackers that can drift apart.
func (s *Service) ActiveBackground(ctx context.Context, parentSessionID string) ([]Survivor, error) {
	rows, err := s.Repo.ListByParent(ctx, parentSessionID)
	if err != nil {
		return nil, err
	}
	var out []Survivor
	// ListByParent is newest first; a banner reads better in start order.
	for i := len(rows) - 1; i >= 0; i-- {
		d := rows[i]
		if !d.Detached || !isLive(d.Status) {
			continue
		}
		out = append(out, Survivor{Handle: d.Handle, ProfileKey: d.ProfileKey, AgentName: d.ChildAgent})
	}
	return out, nil
}

// RecheckBackground is ActiveBackground minus running rows whose sub-agent
// process is gone. A channel asks it when its banner has seen no change for a
// while: a child that died without closing its row would otherwise hold the
// banner until the sweeper got round to it. Queued rows have no process yet
// and are kept. Without AgentAlive wired it is plain ActiveBackground.
func (s *Service) RecheckBackground(ctx context.Context, parentSessionID string) ([]Survivor, error) {
	rows, err := s.Repo.ListByParent(ctx, parentSessionID)
	if err != nil {
		return nil, err
	}
	var out []Survivor
	for i := len(rows) - 1; i >= 0; i-- {
		d := rows[i]
		if !d.Detached || !isLive(d.Status) {
			continue
		}
		if d.Status == entity.DelegationRunning && s.AgentAlive != nil && !s.childAlive(d.ChildSessionID, d.ChildAgent) {
			continue
		}
		out = append(out, Survivor{Handle: d.Handle, ProfileKey: d.ProfileKey, AgentName: d.ChildAgent})
	}
	return out, nil
}

func isLive(status string) bool {
	return status == entity.DelegationQueued || status == entity.DelegationRunning
}

// announceBackground fires the start hook for a background delegation that
// was just accepted, then re-reports the active set. Called once per row, at
// the point Run knows whether it runs now or waits in the queue.
func (s *Service) announceBackground(ctx context.Context, row *entity.AgentDelegation, queued bool) {
	if row.ParentSessionID == "" {
		return
	}
	if s.OnBackgroundStart != nil {
		s.OnBackgroundStart(row.ParentSessionID, BackgroundStart{
			Agent:  Survivor{Handle: row.Handle, ProfileKey: row.ProfileKey, AgentName: row.ChildAgent},
			Task:   clipRunes(row.Task, startTaskRunes),
			Queued: queued,
		})
	}
	s.reportBackground(ctx, row.ParentSessionID)
}

// reportBackground hands the current active background set under
// parentSessionID to OnBackgroundChange, if wired.
func (s *Service) reportBackground(ctx context.Context, parentSessionID string) {
	if s.OnBackgroundChange == nil {
		return
	}
	active, err := s.ActiveBackground(ctx, parentSessionID)
	if err != nil {
		log.Warn().Err(err).Str("session", parentSessionID).
			Msg("delegation: background re-report failed")
		return
	}
	s.OnBackgroundChange(parentSessionID, active)
}

// backgroundEnded re-reports the active set after a background row reached a
// terminal status on a path that bypasses finish (an interrupt, the sweeper).
// Without it the banner would hold until its stale re-check noticed.
func (s *Service) backgroundEnded(ctx context.Context, row *entity.AgentDelegation) {
	if row == nil || !row.Detached || row.ParentSessionID == "" {
		return
	}
	s.reportBackground(context.WithoutCancel(ctx), row.ParentSessionID)
}

// clipRunes shortens s to at most n runes, marking the cut with an ellipsis.
func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
