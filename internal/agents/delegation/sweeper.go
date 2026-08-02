package delegation

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

// StaleClaimSweeper returns board tasks held by vanished workers to the
// queue.
//
// Without it a crashed worker's claim pins its task permanently: the task
// is not unclaimed, so nobody else may take it, and it is not running, so
// nothing will ever finish it. The board slowly fills with work that can
// never be done and gives no signal that anything is wrong.
type StaleClaimSweeper struct {
	Repo *Repo
	// After is how long a claim may sit untouched before it is released.
	After time.Duration
	// Every is the sweep interval.
	Every time.Duration

	stop chan struct{}
}

// NewStaleClaimSweeper builds a sweeper with sane defaults.
func NewStaleClaimSweeper(repo *Repo, after time.Duration) *StaleClaimSweeper {
	if after <= 0 {
		after = 30 * time.Minute
	}
	return &StaleClaimSweeper{
		Repo:  repo,
		After: after,
		// Sweep well inside the staleness window so a released task is
		// picked up promptly, without polling hard enough to matter.
		Every: after / 3,
		stop:  make(chan struct{}),
	}
}

// Start runs the sweeper until Stop is called. Safe to call once.
func (s *StaleClaimSweeper) Start(ctx context.Context) {
	if s.Repo == nil {
		return
	}
	if s.Every <= 0 {
		s.Every = 10 * time.Minute
	}
	go func() {
		t := time.NewTicker(s.Every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stop:
				return
			case <-t.C:
				n, err := s.Repo.ReleaseStaleClaims(ctx, s.After)
				if err != nil {
					log.Warn().Err(err).Msg("delegation: stale-claim sweep failed")
					continue
				}
				if n > 0 {
					log.Info().Int64("released", n).
						Msg("delegation: released task claims from workers that went away")
				}
			}
		}
	}()
}

// Stop halts the sweeper.
func (s *StaleClaimSweeper) Stop() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
}
