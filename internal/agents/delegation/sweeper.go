package delegation

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
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

// DelegationSweeper is the queue's backstop.
//
// The primary mechanism is the poke fired wherever a delegation reaches a
// terminal status; this loop only has to be eventually right. It exists
// for two failure modes the poke cannot cover: a process that died
// between marking a parent blocked and clearing the flag, and a poke lost
// because the goroutine that would have sent it never ran.
//
// Deliberately separate from StaleClaimSweeper, which sweeps task-board
// claims. Same shape, different table, different reason to fire.
type DelegationSweeper struct {
	Svc *Service
	// Every is the sweep interval.
	Every time.Duration

	stop chan struct{}
}

// NewDelegationSweeper builds a sweeper with a sane interval.
func NewDelegationSweeper(svc *Service, every time.Duration) *DelegationSweeper {
	if every <= 0 {
		every = 15 * time.Second
	}
	return &DelegationSweeper{Svc: svc, Every: every, stop: make(chan struct{})}
}

// Start runs the sweeper until Stop is called.
func (d *DelegationSweeper) Start(ctx context.Context) {
	if d.Svc == nil || d.Svc.Repo == nil {
		return
	}
	go func() {
		t := time.NewTicker(d.Every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-d.stop:
				return
			case <-t.C:
				d.Pass(ctx)
			}
		}
	}()
}

// Pass runs one sweep. Exported so a test can drive it without waiting on
// a ticker.
func (d *DelegationSweeper) Pass(ctx context.Context) {
	if n, err := d.Svc.Repo.ClearStaleBlocked(ctx); err != nil {
		log.Warn().Err(err).Msg("delegation: stale-blocked sweep failed")
	} else if n > 0 {
		log.Info().Int64("cleared", n).
			Msg("delegation: released parallel slots held by delegations that had already finished")
	}
	if roots, err := d.Svc.Repo.RootsWithQueued(ctx); err != nil {
		log.Warn().Err(err).Msg("delegation: queued-root sweep failed")
	} else {
		for _, root := range roots {
			d.Svc.startNextQueued(ctx, root)
		}
	}
	d.Svc.expireOverrunningInvestigations(ctx)
	d.Svc.closeAbandonedRuns(ctx)
	d.Svc.nudgeStalledCollects(ctx, d.Every)
}

// closeAbandonedRuns finishes delegations whose agent is gone but whose
// row still says running.
//
// A run ends when await sees the Done event. If that event never arrives
// the wait never returns: the sub-agent answers, writes its transcript,
// goes idle — and the row reads "running, 0/12 turns" forever. The leader
// is never woken, collect keeps reporting pending, and from the UI it is
// indistinguishable from an agent that hung. Observed in the wild.
//
// The dropped-event path that caused it is closed (terminal events no
// longer get discarded when a consumer falls behind, see
// tools/agents.DelegationStream), but this is the backstop for every
// other way a Done can go missing — a crashed reader, a lost
// subscription, a process killed outside the pool. One missed event must
// not strand a delegation permanently.
//
// Deliberately conservative: it only acts when the runtime confirms the
// agent is NOT alive. A slow sub-agent that has simply not spoken in a
// while is still working, and killing it would be worse than the bug.
func (s *Service) closeAbandonedRuns(ctx context.Context) {
	if s.AgentAlive == nil {
		return
	}
	var running []entity.AgentDelegation
	if err := s.Repo.DB().WithContext(ctx).
		Where("status = ?", entity.DelegationRunning).
		Find(&running).Error; err != nil {
		log.Warn().Err(err).Msg("delegation: abandoned-run sweep failed")
		return
	}
	for i := range running {
		row := &running[i]
		if row.ChildSessionID == "" || s.AgentAlive(row.ChildSessionID, row.ChildAgent) {
			continue
		}
		// Whatever it managed to say still counts. Its own progress note
		// is the fallback for a provider that leaves no in-flight buffer.
		out := ""
		if s.Runner != nil {
			out = strings.TrimSpace(s.Runner.PartialText(row.ChildSessionID, row.ChildAgent))
		}
		if out == "" {
			out = strings.TrimSpace(row.LastReport)
		}
		out = s.authoritativeResult(ctx, row, out)

		ok, err := s.Repo.FinishGuarded(ctx, row.ID,
			entity.DelegationRunning, entity.DelegationDone, out, "", row.TurnsUsed)
		if err != nil || !ok {
			continue
		}
		log.Warn().
			Str("delegation", row.ID).
			Str("child_session", row.ChildSessionID).
			Msg("delegation: agent had already exited but the run never closed; finishing it and delivering the result")

		// Deliver, or the leader still never learns the work finished —
		// which is the half of the bug that actually costs the user.
		fresh, ferr := s.Repo.Get(ctx, row.ID)
		if ferr != nil {
			continue
		}
		s.deliver(ctx, fresh, fresh.DeliverySink, s.doneResult(ctx, fresh, fresh.TurnsUsed, fresh.TokensUsed, out))
		s.pokeSlot(fresh.RootID)
	}
}

// expireOverrunningInvestigations stops investigations that have run past
// the wall-clock cap.
//
// Wall clock catches what turn counting cannot: an agent that is slow
// rather than chatty. Partial results are kept — an investigation that
// ran out of time still established whatever it established.
func (s *Service) expireOverrunningInvestigations(ctx context.Context) {
	lim := s.limits().normalize()
	cutoff := time.Now().Add(-time.Duration(lim.MaxRuntimeMinutes) * time.Minute)

	var stale []entity.AgentIncident
	if err := s.Repo.DB().WithContext(ctx).
		Where("status = ? AND created_at < ?", entity.IncidentInvestigating, cutoff).
		Find(&stale).Error; err != nil {
		log.Warn().Err(err).Msg("delegation: runtime-cap sweep failed")
		return
	}
	for i := range stale {
		inc := &stale[i]
		s.stopInvestigation(ctx, inc, entity.IncidentEscalated,
			fmt.Sprintf("runtime cap reached (%d minutes)", lim.MaxRuntimeMinutes))
		// Stop the work too, or the tree keeps spending on an
		// investigation nobody is going to act on.
		rows, err := s.Repo.ListActiveByRoot(ctx, inc.RootID)
		if err != nil {
			continue
		}
		for _, row := range rows {
			if _, err := s.Interrupt(ctx, row.ID, row.TriggeredBy, true); err != nil {
				log.Debug().Err(err).Str("delegation", row.ID).
					Msg("delegation: runtime-cap stop failed")
			}
		}
	}
}

// nudgeStalledCollects wakes a leader whose async result landed while it
// was away.
//
// delivery_sink=session covers the normal case. It fails in exactly one:
// the leader had already exited, so the wake had nowhere to go and the
// result sits collected-by-nobody. One nudge per delegation, marked, so a
// sweep every fifteen seconds does not become a wake every fifteen
// seconds.
func (s *Service) nudgeStalledCollects(ctx context.Context, grace time.Duration) {
	if s.Deliver == nil {
		return
	}
	if grace <= 0 {
		grace = time.Minute
	}
	cutoff := time.Now().Add(-grace)

	var rows []entity.AgentDelegation
	if err := s.Repo.DB().WithContext(ctx).
		Where("mode = ? AND delivery_sink = ? AND collected = ? AND collect_nudged = ? AND ended_at IS NOT NULL AND ended_at < ?",
			ModeAsync, SinkSession, false, false, cutoff).
		Limit(50).Find(&rows).Error; err != nil {
		log.Warn().Err(err).Msg("delegation: stalled-collect sweep failed")
		return
	}
	for i := range rows {
		row := &rows[i]
		if err := s.Repo.MarkCollectNudged(ctx, row.ID); err != nil {
			continue
		}
		text := fmt.Sprintf(
			"@%s finished while you were away. Collect delegation %s to pick up its result.",
			row.Handle, row.ID)
		if err := s.Deliver.DeliverToSession(ctx, row.ParentSessionID, row.ParentAgent, text); err != nil {
			log.Debug().Err(err).Str("delegation", row.ID).
				Msg("delegation: stalled-collect nudge not delivered")
		}
	}
}

// Stop halts the sweeper.
func (d *DelegationSweeper) Stop() {
	select {
	case <-d.stop:
	default:
		close(d.stop)
	}
}

// Stop halts the sweeper.
func (s *StaleClaimSweeper) Stop() {
	select {
	case <-s.stop:
	default:
		close(s.stop)
	}
}
