package schedule

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/pkg/upgrade"
)

// Sender delivers one message into a session, and materializes that session
// when a project-scoped schedule targets an id that may not exist yet.
// Satisfied by *pool.Pool (SendWithProject is the same path channels use;
// EnsureSession is what workflow's session_init calls); kept as an interface
// so the runner is testable without a real pool.
type Sender interface {
	SendWithProject(ctx context.Context, sessionID, agentName, source, role, text, projectID string) error
	EnsureSession(ctx context.Context, sessionID, source, projectID string) error
	// EnsureSessionOwner attaches an identity to the target session. Without
	// it a scheduled fire runs ownerless, and an ownerless session mints no
	// per-user MCP credential — the spawn falls back to the synthetic
	// internal principal, which holds no access tags. That is how a job that
	// worked when its creator ran it by hand comes back seeing almost
	// nothing once it runs on a timer.
	EnsureSessionOwner(ctx context.Context, sessionID, userID string)
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
	// active counts deliveries in flight, for the drain tracker.
	active atomic.Int64
	// runAsUsable reports whether a user id may still be run as — they
	// exist and are approved. Checked at FIRE time, not create time,
	// because the gap between the two is where accounts get disabled.
	//
	// Takes the tick's ctx: this runs a DB lookup, and a tick delivers a
	// BATCH. Handing it a detached context meant one unreachable database
	// could park the runner forever and hold up every other schedule due in
	// the same tick — a lookup that guards one fire must not be able to
	// stop all of them.
	//
	// nil disables the check (stdio, tests), which is safe there: without a
	// server there is no MCP credential to mint in the first place.
	runAsUsable func(ctx context.Context, userID string) bool
	// connExec runs a watch's connector steps; nil fails them (stdio,
	// tests that don't need one).
	connExec ConnectorExecutor
	// watchSem bounds how many watch runs execute at once. A watch run can
	// take up to a couple of minutes (a slow script), so they run off the
	// tick loop — this keeps a burst of them from piling onto the host.
	watchSem chan struct{}
	// testSem bounds dry runs (test) to one at a time, apart from watchSem:
	// tests never take a slot a live watch is waiting for.
	testSem chan struct{}
	// watches is the in-memory watch scheduler state (watch_runner.go).
	watches *watchSet
}

func NewRunner(store *Store, sender Sender, layout agentconfig.Layout) *Runner {
	r := &Runner{store: store, sender: sender, layout: layout, wake: make(chan struct{}, 1), watchSem: make(chan struct{}, watchConcurrency), testSem: make(chan struct{}, 1), watches: newWatchSet()}
	// A due row is claimed in the DB before delivery, so a delivery abandoned
	// mid-flight is LOST rather than retried. That makes it worth draining.
	upgrade.Register("scheduled messages", r.ActiveCount)
	return r
}

// WithRunAsCheck installs the fire-time guard on the run-as identity. The
// server passes a lookup over its user store; callers without one (tests,
// stdio) simply skip the check.
func (r *Runner) WithRunAsCheck(usable func(ctx context.Context, userID string) bool) *Runner {
	r.runAsUsable = usable
	return r
}

// ActiveCount is how many due messages are being delivered right now.
func (r *Runner) ActiveCount() int {
	if r == nil {
		return 0
	}
	return int(r.active.Load())
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
	r.refreshWatches(ctx, l)
	r.tickWatch(ctx, l)
	defer r.stopWatches(l)
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	// Watches get their own, finer tick: their floor is 10s, which a 30s
	// poll would turn into 30s. A watch tick is memory only — the DB is read
	// on the refresh, which rides the 30s ticker.
	wt := time.NewTicker(watchPollInterval)
	defer wt.Stop()
	for {
		select {
		case <-ctx.Done():
			l.Info().Msg("stopped")
			return
		case <-t.C:
			r.tick(ctx, l)
			r.refreshWatches(ctx, l)
		case <-wt.C:
			r.tickWatch(ctx, l)
		case <-r.wake:
			// Something was edited or made due right now; don't make the
			// caller wait out the remaining poll interval.
			r.tick(ctx, l)
			r.refreshWatches(ctx, l)
			r.tickWatch(ctx, l)
		}
	}
}

// tick claims every currently-due schedule and delivers it. Uses the wall
// clock; the store's atomic claim guarantees each row fires at most once
// even across overlapping ticks or a second wick instance.
func (r *Runner) tick(ctx context.Context, l zerologLogger) {
	// A process handing over to a successor fires nothing new: every fire
	// starts a turn the drain then has to wait for, so a short recurring
	// schedule would keep it from ever settling. The rows stay due and the
	// successor claims them once it holds the intake baton.
	if upgrade.Draining() {
		return
	}
	now := time.Now()
	due, err := r.store.ClaimDue(ctx, now, claimBatch)
	if err != nil {
		l.Warn().Err(err).Msg("claim due schedules failed")
		return
	}
	for i := range due {
		r.active.Add(1)
		r.deliver(ctx, l, due[i])
		r.active.Add(-1)
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
	target, serr := r.sendToTarget(ctx, l, m, m.Message)
	// Every fire leaves a run record, delivered or not — the history the
	// Scheduled page and wick_schedule_message action=runs read.
	if dir := WatchRunsDir(r.layout, m); dir != "" {
		rec := messageRunRecord(m, firedAt, target, serr)
		if werr := writeRun(dir, &rec); werr != nil {
			l.Warn().Str("id", m.ID).Err(werr).Msg("write run history failed")
		}
	}
	if serr != nil {
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

// sendToTarget resolves m's target session and injects text into it — the
// delivery half of a fire, shared by a message schedule (every fire) and a
// watch (only its match / failure notice). Any failure marks the row failed
// and is returned, so the run history can say why.
func (r *Runner) sendToTarget(ctx context.Context, l zerologLogger, m entity.ScheduledMessage, text string) (string, error) {
	target, mint, err := ResolveTarget(m, time.Now())
	if err != nil {
		l.Warn().Str("id", m.ID).Str("mode", m.Mode()).Err(err).Msg("resolve target failed")
		failMsg := "resolve target: " + err.Error()
		_ = r.store.MarkFailed(ctx, m.ID, failMsg)
		return "", errors.New(failMsg)
	}

	projectID := m.ProjectID
	if mint {
		// Idempotent: creates the session when absent, reuses it when the
		// rendered/generated id already exists.
		if err := r.sender.EnsureSession(ctx, target, deliverySource, m.ProjectID); err != nil {
			l.Warn().Str("id", m.ID).Str("session", target).Err(err).Msg("ensure target session failed")
			failMsg := "ensure session " + target + ": " + err.Error()
			_ = r.store.MarkFailed(ctx, m.ID, failMsg)
			return "", errors.New(failMsg)
		}
	} else {
		sess, lerr := session.Load(r.layout, target)
		if lerr != nil {
			l.Warn().Str("id", m.ID).Str("session", target).Err(lerr).Msg("target session not found")
			failMsg := "target session not found: " + lerr.Error()
			_ = r.store.MarkFailed(ctx, m.ID, failMsg)
			return "", errors.New(failMsg)
		}
		projectID = sess.Meta.ProjectID
	}

	// Attach the identity BEFORE sending, because the send is what spawns the
	// agent and the spawn mints its MCP credential from the session's owner.
	// Stamping afterwards would be a turn too late: that run would already be
	// executing as the synthetic internal principal.
	//
	// EnsureSessionOwner is first-writer-wins, so a schedule pointed at a
	// session somebody else already owns does not take it over — that run
	// stays on the session owner's identity rather than the schedule's.
	// A run-as user who has since been removed or un-approved must STOP the
	// fire, not quietly downgrade it. Falling through would hand the run to
	// the synthetic internal principal — an admin-role identity carrying no
	// access tags — so a revoked account would turn into "runs as something
	// else entirely", silently, on a timer. Better a failed row somebody can
	// see than a job that keeps running under an identity nobody chose.
	if runAs := m.EffectiveRunAsUser(); runAs != "" {
		if r.runAsUsable != nil && !r.runAsUsable(ctx, runAs) {
			l.Warn().Str("id", m.ID).Str("run_as", runAs).
				Msg("run-as user is gone or not approved; refusing to fire")
			failMsg := "run-as user " + runAs + " is missing or not approved"
			_ = r.store.MarkFailed(ctx, m.ID, failMsg)
			return "", errors.New(failMsg)
		}
		r.sender.EnsureSessionOwner(ctx, target, runAs)
	}

	if err := r.sender.SendWithProject(ctx, target, m.AgentName, deliverySource, "user", text, projectID); err != nil {
		l.Warn().Str("id", m.ID).Str("session", target).Err(err).Msg("deliver failed")
		failMsg := err.Error()
		_ = r.store.MarkFailed(ctx, m.ID, failMsg)
		return "", errors.New(failMsg)
	}
	notifyFired(ctx, m, target)
	return target, nil
}

// FiredHook is told about every successful delivery: the row as claimed
// and the session the message landed in. Used to record a visible
// "scheduled ran" event in that chat. Runs synchronously after the send,
// so it must be cheap.
type FiredHook func(ctx context.Context, m entity.ScheduledMessage, sessionID string)

var firedHook atomic.Pointer[FiredHook]

// SetFiredHook installs (or, with nil, removes) the process-wide FiredHook.
func SetFiredHook(fn FiredHook) {
	if fn == nil {
		firedHook.Store(nil)
		return
	}
	firedHook.Store(&fn)
}

func notifyFired(ctx context.Context, m entity.ScheduledMessage, sessionID string) {
	if fn := firedHook.Load(); fn != nil {
		(*fn)(ctx, m, sessionID)
	}
}

// zerologLogger is a local alias so the tick/deliver signatures read
// cleanly.
type zerologLogger = zerolog.Logger
