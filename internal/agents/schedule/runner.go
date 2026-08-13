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

// Sender delivers one message into a session, and materializes that session
// when a project-scoped schedule targets an id that may not exist yet.
// Satisfied by *pool.Pool (SendWithProject is the same path channels use;
// EnsureSession is what workflow's session_init calls); kept as an interface
// so the runner is testable without a real pool.
type Sender interface {
	SendWithProject(ctx context.Context, sessionID, agentName, source, role, text, projectID string) error
	EnsureSession(ctx context.Context, sessionID, source, projectID string) error
}

// deliverySource tags turns injected by the scheduler, and doubles as the
// Origin stamped on sessions the scheduler mints.
const deliverySource = "schedule"

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
	// wake lets a manual run (Store.RunNow) collapse the poll delay: without
	// it a "run now" would still sit until the next 30s tick, which is most
	// of the point of asking. Buffered + non-blocking send, so a burst of
	// nudges coalesces into one extra tick.
	wake chan struct{}
}

func NewRunner(store *Store, sender Sender, layout agentconfig.Layout) *Runner {
	return &Runner{store: store, sender: sender, layout: layout, wake: make(chan struct{}, 1)}
}

// Wake asks the runner to poll immediately instead of waiting for the next
// tick. Safe from any goroutine, and a no-op when a wake is already pending.
func (r *Runner) Wake() {
	if r == nil || r.wake == nil {
		return
	}
	select {
	case r.wake <- struct{}{}:
	default: // one queued wake is enough
	}
}

// running is the runner started by the HTTP server, so a "run now" from a
// handler can poke it. Package-level because the alternative — threading a
// *Runner through the MCP handler and the tools router — would touch a lot of
// wiring for one optional nudge. nil outside the server (stdio, tests), where
// WakeRunner is simply a no-op and the manual run just waits for the poll.
var running *Runner

// WakeRunner pokes the process's schedule runner, if one is running, so a row
// just made due (Store.RunNow) is delivered now rather than up to a poll
// interval later. Safe to call when no runner exists.
func WakeRunner() { running.Wake() }

// Run blocks until ctx is cancelled, delivering due schedules every tick.
// It fires once immediately on start so schedules that came due during
// downtime are not delayed a full interval.
func (r *Runner) Run(ctx context.Context) {
	l := log.With().Str("component", "schedule-runner").Logger()
	l.Info().Dur("interval", pollInterval).Msg("started")

	// Publish for WakeRunner. Set here (not in NewRunner) so a runner built
	// in a test — which never calls Run — can't become the process-wide one.
	running = r
	defer func() { running = nil }()

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
		case <-r.wake:
			// A manual run made something due right now; don't make the
			// caller wait out the remaining poll interval.
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

// deliver injects one claimed schedule's message into its target session,
// then sets the row's next state: a one-shot finishes (done); a recurring
// schedule is rescheduled to its next fire (or finishes on max_runs /
// ends_at).
//
// The target depends on the schedule's session mode (see target.go):
// "existing" delivers into the fixed session and resolves the project LIVE
// from that session's meta (not cached at create time), so a session that
// moved projects still lands in the right cwd; the project-scoped modes mint
// or reuse a session in the schedule's own project.
//
// A send failure records the error and stops the schedule (recurring
// included). So does a vanished session — but only in "existing" mode: a
// project-scoped schedule creates its target, so it cannot be orphaned by
// session reaping, which is the whole point of the mode.
func (r *Runner) deliver(ctx context.Context, l zerologLogger, m entity.ScheduledMessage) {
	firedAt := time.Now()

	target, mint, err := ResolveTarget(m, firedAt)
	if err != nil {
		l.Warn().Str("id", m.ID).Str("mode", m.Mode()).Err(err).Msg("resolve target failed")
		_ = r.store.MarkFailed(ctx, m.ID, "resolve target: "+err.Error())
		return
	}

	projectID := m.ProjectID
	if mint {
		// Idempotent: creates the session when absent, reuses it when the
		// rendered/generated id already exists.
		if err := r.sender.EnsureSession(ctx, target, deliverySource, m.ProjectID); err != nil {
			l.Warn().Str("id", m.ID).Str("session", target).Err(err).Msg("ensure target session failed")
			_ = r.store.MarkFailed(ctx, m.ID, "ensure session "+target+": "+err.Error())
			return
		}
	} else {
		sess, lerr := session.Load(r.layout, target)
		if lerr != nil {
			l.Warn().Str("id", m.ID).Str("session", target).Err(lerr).Msg("target session not found")
			_ = r.store.MarkFailed(ctx, m.ID, "target session not found: "+lerr.Error())
			return
		}
		projectID = sess.Meta.ProjectID
	}

	if err := r.sender.SendWithProject(ctx, target, m.AgentName, deliverySource, "user", m.Message, projectID); err != nil {
		l.Warn().Str("id", m.ID).Str("session", target).Err(err).Msg("deliver failed")
		_ = r.store.MarkFailed(ctx, m.ID, err.Error())
		return
	}

	// A manual run (run_now) is an EXTRA fire, not the next scheduled one: it
	// puts the real next fire back exactly where it was, so testing a
	// schedule can't shift its cadence or spend its max_runs. The claim
	// already skipped run_count for it.
	if m.ManualFire {
		next := time.Time{}
		if m.PendingRunAt != nil {
			next = *m.PendingRunAt
		}
		// A one-shot fired manually still has its own fire pending, so it is
		// NOT finished here — Finalize keeps it live because `next` is set.
		if err := r.store.Finalize(ctx, m.ID, m.Kind, next, target); err != nil {
			l.Warn().Str("id", m.ID).Err(err).Msg("finalize (manual) failed")
			return
		}
		l.Info().Str("id", m.ID).Str("session", target).Time("next", next).
			Msg("delivered (manual run); schedule unchanged")
		return
	}

	// Success — set the next state. m.RunCount was already incremented by the
	// claim, so it reflects fires completed (this one included).
	if m.IsRecurring() {
		next, aerr := advance(m, firedAt, m.RunCount)
		if aerr != nil {
			// A bad cron/interval can't be advanced — stop rather than spin.
			l.Warn().Str("id", m.ID).Err(aerr).Msg("advance failed; finishing schedule")
			_ = r.store.Finalize(ctx, m.ID, m.Kind, time.Time{}, target)
			return
		}
		if err := r.store.Finalize(ctx, m.ID, m.Kind, next, target); err != nil {
			l.Warn().Str("id", m.ID).Err(err).Msg("finalize (recurring) failed")
			return
		}
		if next.IsZero() {
			l.Info().Str("id", m.ID).Str("session", target).Msg("delivered; recurring finished (stop condition)")
		} else {
			l.Info().Str("id", m.ID).Str("session", target).Time("next", next).Msg("delivered; rescheduled")
		}
		return
	}

	if err := r.store.Finalize(ctx, m.ID, m.Kind, time.Time{}, target); err != nil {
		l.Warn().Str("id", m.ID).Err(err).Msg("finalize (once) failed")
		return
	}
	l.Info().Str("id", m.ID).Str("session", target).Msg("delivered")
}

// zerologLogger is a local alias so the tick/deliver signatures read
// cleanly.
type zerologLogger = zerolog.Logger
