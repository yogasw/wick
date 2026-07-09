package schedule

import (
	"context"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

// Sender delivers one message into a session. Satisfied by *pool.Pool's
// SendWithProject (the same path channels use); kept as an interface so the
// runner is testable without a real pool.
type Sender interface {
	SendWithProject(ctx context.Context, sessionID, agentName, source, role, text, projectID string) error
}

// pollInterval is how often the runner scans for due schedules. 30s matches
// the channel-config watcher cadence; hour-scale "check back later" nudges
// don't need finer granularity, and boot recovery is just the first tick
// picking up everything whose run_at passed while wick was down.
const pollInterval = 30 * time.Second

// claimBatch caps how many due schedules one tick delivers, so a backlog
// (e.g. after a long downtime) drains in bounded chunks instead of one huge
// burst into the pool.
const claimBatch = 50

// Runner polls the store for due schedules and delivers each through the
// pool. One goroutine, started from the HTTP server (where the pool lives).
type Runner struct {
	store  *Store
	sender Sender
	layout agentconfig.Layout
}

func NewRunner(store *Store, sender Sender, layout agentconfig.Layout) *Runner {
	return &Runner{store: store, sender: sender, layout: layout}
}

// Run blocks until ctx is cancelled, delivering due schedules every tick.
// It fires once immediately on start so schedules that came due during
// downtime are not delayed a full interval.
func (r *Runner) Run(ctx context.Context) {
	l := log.With().Str("component", "schedule-runner").Logger()
	l.Info().Dur("interval", pollInterval).Msg("started")

	r.tick(ctx, l)
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			l.Info().Msg("stopped")
			return
		case <-t.C:
			r.tick(ctx, l)
		}
	}
}

// tick claims every currently-due schedule and delivers it. Uses the wall
// clock; the store's atomic claim guarantees each row fires at most once
// even across overlapping ticks or a second wick instance.
func (r *Runner) tick(ctx context.Context, l zerologLogger) {
	now := time.Now()
	due, err := r.store.ClaimDue(ctx, now, claimBatch)
	if err != nil {
		l.Warn().Err(err).Msg("claim due schedules failed")
		return
	}
	for i := range due {
		r.deliver(ctx, l, due[i])
	}
}

// deliver injects one claimed schedule's message into its session, then sets
// the row's next state: a one-shot finishes (done); a recurring schedule is
// rescheduled to its next fire (or finishes on max_runs / ends_at). The
// project id is resolved LIVE from the target session's meta (not cached at
// create time) so a session that moved projects still delivers to the right
// cwd. A send failure — or a vanished session — records the error and stops
// the schedule (recurring included): "session gone → error + cancel".
func (r *Runner) deliver(ctx context.Context, l zerologLogger, m entity.ScheduledMessage) {
	firedAt := time.Now()

	sess, err := session.Load(r.layout, m.SessionID)
	if err != nil {
		l.Warn().Str("id", m.ID).Str("session", m.SessionID).Err(err).Msg("target session not found")
		_ = r.store.MarkFailed(ctx, m.ID, "target session not found: "+err.Error())
		return
	}

	if err := r.sender.SendWithProject(ctx, m.SessionID, m.AgentName, "schedule", "user", m.Message, sess.Meta.ProjectID); err != nil {
		l.Warn().Str("id", m.ID).Str("session", m.SessionID).Err(err).Msg("deliver failed")
		_ = r.store.MarkFailed(ctx, m.ID, err.Error())
		return
	}

	// Success — set the next state. m.RunCount was already incremented by the
	// claim, so it reflects fires completed (this one included).
	if m.IsRecurring() {
		next, aerr := advance(m, firedAt, m.RunCount)
		if aerr != nil {
			// A bad cron/interval can't be advanced — stop rather than spin.
			l.Warn().Str("id", m.ID).Err(aerr).Msg("advance failed; finishing schedule")
			_ = r.store.Finalize(ctx, m.ID, true, time.Time{})
			return
		}
		if err := r.store.Finalize(ctx, m.ID, true, next); err != nil {
			l.Warn().Str("id", m.ID).Err(err).Msg("finalize (recurring) failed")
			return
		}
		if next.IsZero() {
			l.Info().Str("id", m.ID).Str("session", m.SessionID).Msg("delivered; recurring finished (stop condition)")
		} else {
			l.Info().Str("id", m.ID).Str("session", m.SessionID).Time("next", next).Msg("delivered; rescheduled")
		}
		return
	}

	if err := r.store.Finalize(ctx, m.ID, false, time.Time{}); err != nil {
		l.Warn().Str("id", m.ID).Err(err).Msg("finalize (once) failed")
		return
	}
	l.Info().Str("id", m.ID).Str("session", m.SessionID).Msg("delivered")
}

// zerologLogger is a local alias so the tick/deliver signatures read
// cleanly.
type zerologLogger = zerolog.Logger
