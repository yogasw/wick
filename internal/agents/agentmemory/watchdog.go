package agentmemory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// The watchdog (PLAN §25).
//
// The failure it exists for is a silent one: when the daemon is down, every
// agent keeps working, keeps answering, and quietly recalls and records
// nothing. No error is raised anywhere, so the first sign is a week of memory
// that was never written.
//
// It supervises, it does not decide. Whether a daemon SHOULD be running is
// already answered by the effective-autostart signal — stored autostart, or
// the lock a provider instance holds (§10.3). A second switch of its own
// could disagree with that one, and two switches that disagree about whether
// a process should exist is the bug, not the feature (§25.1).
//
// Everything it does is counted and shown. A daemon restarted forty times a
// day is a bug someone has to see; a watchdog that hides it by succeeding
// every time has failed at the only thing it is for (§25.3 guard 3).

const (
	// watchInterval is how often the states are checked. Half a minute is
	// short enough that a dead daemon costs one agent turn's worth of
	// memory at most, and long enough that the probe itself is nothing on
	// a 2-vCPU host: one loopback GET per backend per 30s.
	watchInterval = 30 * time.Second

	// hungGrace is how long a LIVE process may fail its health probe
	// before it is called hung. Three missed probes rather than one: a
	// single failure is a daemon busy with a compaction or a slow first
	// query, and restarting on that would interrupt the very work it was
	// doing.
	hungGrace = 90 * time.Second

	// watchStartTimeout bounds one supervised start attempt. It matches
	// the panel's own start timeout — the watchdog must not be more
	// patient with a daemon than a person watching a spinner is.
	watchStartTimeout = startTimeout

	// watchBackoffMin / watchBackoffMax bound the wait between attempts,
	// doubling in between: 15s, 30s, 1m, 2m, 4m (capped at 5m). The first
	// wait is short because the common failure — a port held by something
	// that is about to go away — clears in seconds; the cap is there
	// because nothing that has failed five times gets fixed by trying
	// faster.
	watchBackoffMin = 15 * time.Second
	watchBackoffMax = 5 * time.Minute

	// watchGiveUpAfter is how many consecutive failed attempts end the
	// retrying. A daemon that cannot start at all — binary gone, port
	// permanently taken, store corrupt — would otherwise be respawned
	// forever on a host with 2 vCPU, and the operator would see a busy
	// machine rather than a stated problem (§25.3 guard 2).
	watchGiveUpAfter = 5
)

// Watchdog reasons — why an intervention happened, in the words the panel
// shows. They are kept apart because they mean different things: dead is a
// daemon that exited, hung is one that is running and not answering, and
// off is one that was never started. Collapsing them into "restarted" is
// exactly the information loss §25.2 warns about.
const (
	ReasonDead = "dead"
	ReasonHung = "hung"
	ReasonOff  = "off"
)

// WatchdogState is one backend's supervision record, as the panel reads it.
//
// It is deliberately a record of what HAPPENED, not just of what is: a
// watchdog whose only output is "everything is fine" cannot be told apart
// from one that is not running.
type WatchdogState struct {
	// Watching is true when this backend is supervised — the effective
	// autostart signal, nothing else.
	Watching bool `json:"watching"`
	// Restarts counts every intervention since wick started, and
	// HungRestarts the subset that were wedged daemons. The split is the
	// point: a daemon that keeps hanging is a different bug from one
	// that keeps exiting.
	Restarts     int `json:"restarts"`
	HungRestarts int `json:"hung_restarts"`
	// LastReason is one of ReasonDead / ReasonHung / ReasonOff, and
	// LastRestartMS when it happened (unix ms).
	LastReason    string `json:"last_reason,omitempty"`
	LastRestartMS int64  `json:"last_restart_ms,omitempty"`
	// LastError is the failure of the most recent attempt, "" when the
	// last attempt worked.
	LastError string `json:"last_error,omitempty"`
	// ConsecutiveFailures drives the backoff and the give-up. Reset by
	// any healthy check, so a daemon that comes back is forgiven.
	ConsecutiveFailures int `json:"consecutive_failures"`
	// NextAttemptMS is when the backoff allows the next try (unix ms), 0
	// when nothing is being waited for.
	NextAttemptMS int64 `json:"next_attempt_ms,omitempty"`
	// GaveUp, with the reason, is the stated end of retrying. It clears
	// the moment the daemon is healthy again — by someone's hand or by a
	// fixed binary — because the watchdog's opinion is about now, not
	// about what went wrong an hour ago.
	GaveUp       bool   `json:"gave_up"`
	GaveUpReason string `json:"gave_up_reason,omitempty"`
	// StoppedByOperator is why an otherwise-supervised daemon is left
	// down. Shown so "nothing is happening" is never a mystery.
	StoppedByOperator bool `json:"stopped_by_operator"`
	// unhealthySince is when a live process first failed its probe. Not
	// serialised: it is the hung timer, and the panel reads the outcome.
	unhealthySince time.Time
}

// watchTarget is what the watchdog needs from one backend. An interface
// rather than *Manager so the supervision rules can be tested against a
// double — a watchdog tested by spawning real daemons is a watchdog nobody
// runs the tests for.
type watchTarget interface {
	// ID names the backend, for the state map and the log.
	ID() string
	// Supervised reports whether this backend should be running at all.
	Supervised() bool
	// Managed is true while wick holds a process for it, Alive whether
	// that process still exists, and Healthy whether SOMETHING answers
	// the health path (including a daemon wick did not spawn).
	Managed() bool
	Alive() bool
	Healthy() bool
	// OperatorStopped reports a deliberate Stop.
	OperatorStopped() bool
	// Start and Restart are the two interventions.
	Start(ctx context.Context) error
	Restart(ctx context.Context) error
}

// backendTarget adapts a registered backend to watchTarget.
type backendTarget struct{ be *Backend }

func (t backendTarget) ID() string { return t.be.Desc.ID }

// Supervised is the effective autostart signal and nothing else — the same
// answer the Overview's autostart control shows (§25.1).
func (t backendTarget) Supervised() bool {
	if store == nil || !store.Enabled() {
		return false
	}
	return settingsFor(t.be.Desc.ID).EffectiveAutostart()
}

func (t backendTarget) Managed() bool { return t.be.Mgr.spawnedHere() }

// Alive is whether a process of this backend EXISTS, whoever started it.
//
// It used to be ChildAlive alone, which is only ever true for a daemon this
// wick process spawned. After a handover that is false for a daemon that is
// very much running, so an adopted daemon that stopped answering read as
// "dead" — and dead means Start, which on a host that already has one running
// is how you end up with two daemons on two stores. The process list is asked
// instead (adopt.go).
func (t backendTarget) Alive() bool {
	return t.be.Mgr.ChildAlive() || len(t.be.Mgr.Daemons()) > 0
}
func (t backendTarget) OperatorStopped() bool { return t.be.Mgr.OperatorStopped() }

// Healthy is the UNCACHED probe. The cached one exists so a page poll does
// not hammer the daemon; here a stale "it was fine five seconds ago" is the
// one answer that must not be trusted.
func (t backendTarget) Healthy() bool { return t.be.Mgr.probeHealth() }

func (t backendTarget) Start(ctx context.Context) error   { return t.be.Mgr.StartAndWait(ctx) }
func (t backendTarget) Restart(ctx context.Context) error { return t.be.Mgr.Restart(ctx) }

// Watchdog supervises every registered backend on a fixed interval.
//
// The clock and the target list are fields rather than package calls so the
// whole of it is testable without sleeping or spawning: a test drives Tick
// directly and moves the clock by hand.
type Watchdog struct {
	mu     sync.Mutex
	states map[string]*WatchdogState

	// targets supplies what to supervise. Default: the registry.
	targets func() []watchTarget
	// now is the clock. Default: time.Now.
	now func() time.Time
	// upgrading reports wick's own graceful-upgrade window. Default: the
	// injected hook (see SetUpgradeWindow), which is false until wired.
	upgrading func() bool

	stop chan struct{}
	done chan struct{}
}

// NewWatchdog builds a watchdog over the registered backends.
func NewWatchdog() *Watchdog {
	return &Watchdog{
		states:    map[string]*WatchdogState{},
		targets:   registryTargets,
		now:       time.Now,
		upgrading: upgradeInFlight,
	}
}

func registryTargets() []watchTarget {
	out := make([]watchTarget, 0, len(order))
	for _, be := range List() {
		out = append(out, backendTarget{be: be})
	}
	return out
}

// upgradeWindow reports whether wick is handing over to a successor. A var,
// injected by the hosting package, for the same reason loadInstances is:
// this package does not import wick's server internals, and the rule has to
// be testable without one. Unwired = never upgrading, which is the right
// default for a process that has no handover machinery at all.
var upgradeWindow func() bool

// SetUpgradeWindow wires the graceful-upgrade signal. Called once at boot.
func SetUpgradeWindow(fn func() bool) { upgradeWindow = fn }

func upgradeInFlight() bool { return upgradeWindow != nil && upgradeWindow() }

// Tick is one supervision pass. Exported for tests and for a caller that
// wants an immediate check rather than waiting out the interval.
//
// A pass during wick's own handover does NOTHING — not even bookkeeping. The
// successor adopts the running daemon and the outgoing process is about to
// exit, so anything decided here is decided about a daemon that is no longer
// this process's to manage, and acting on it would thrash on every reload
// (§25.3 guard 5).
func (w *Watchdog) Tick(ctx context.Context) {
	if w.upgrading != nil && w.upgrading() {
		return
	}
	for _, t := range w.targets() {
		w.check(ctx, t)
	}
}

// check supervises one backend.
func (w *Watchdog) check(ctx context.Context, t watchTarget) {
	st := w.state(t.ID())

	supervised := t.Supervised()
	w.mu.Lock()
	st.Watching = supervised
	st.StoppedByOperator = t.OperatorStopped()
	w.mu.Unlock()

	if !supervised {
		// Nothing depends on this daemon, so there is nothing to keep
		// alive. The counters stay as they are — they are a record.
		return
	}
	// A person stopped it. The watchdog's job is to keep a daemon running
	// that SHOULD be; a deliberate Stop is the statement that it should
	// not (§25.3 guard 1).
	if t.OperatorStopped() {
		return
	}

	healthy := t.Healthy()
	if healthy {
		w.recovered(st)
		return
	}

	reason := ReasonOff
	switch {
	case t.Alive():
		// Alive and silent. Give it the grace window first: one missed
		// probe is a busy daemon, not a wedged one.
		//
		// Managed is deliberately NOT part of this any more. A wedged daemon
		// is wedged whoever started it, and the restart path knows how to
		// stop an adopted one (Manager.stopAdopted). Requiring ours here is
		// what left an inherited daemon hung forever with the watchdog
		// reporting nothing.
		if !w.hungLongEnough(st) {
			return
		}
		reason = ReasonHung
	case t.Managed():
		// wick holds a process record and the process is gone.
		reason = ReasonDead
	}

	if !w.mayAttempt(st) {
		return
	}
	w.intervene(ctx, t, st, reason)
}

// hungLongEnough starts (and reads) the hung timer. It returns true only
// once a live process has been silent for the whole grace window.
func (w *Watchdog) hungLongEnough(st *WatchdogState) bool {
	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	if st.unhealthySince.IsZero() {
		st.unhealthySince = now
		return false
	}
	return now.Sub(st.unhealthySince) >= hungGrace
}

// recovered records a healthy check: the hung timer and the failure streak
// are cleared, and a give-up is withdrawn. A daemon that answers is a daemon
// the watchdog has no opinion about.
func (w *Watchdog) recovered(st *WatchdogState) {
	w.mu.Lock()
	defer w.mu.Unlock()
	st.unhealthySince = time.Time{}
	st.ConsecutiveFailures = 0
	st.NextAttemptMS = 0
	st.LastError = ""
	st.GaveUp = false
	st.GaveUpReason = ""
}

// mayAttempt applies the give-up and the backoff.
func (w *Watchdog) mayAttempt(st *WatchdogState) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if st.GaveUp {
		return false
	}
	return st.NextAttemptMS == 0 || w.now().UnixMilli() >= st.NextAttemptMS
}

// intervene performs one start or restart and records the outcome.
func (w *Watchdog) intervene(ctx context.Context, t watchTarget, st *WatchdogState, reason string) {
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), watchStartTimeout)
	defer cancel()

	logger := log.With().Str("component", "agentmemory").Str("backend", t.ID()).Logger()
	logger.Warn().Str("reason", reason).Msg("agentmemory: watchdog intervening")

	var err error
	if reason == ReasonOff {
		// Nothing is held and nothing is running: a plain start.
		err = t.Start(actx)
	} else {
		// Dead and hung BOTH go through restart, and dead is the
		// subtle one: wick still holds the process record, and start
		// returns "already running" on a record whose process is gone
		// — the watchdog would count a success and spawn nothing.
		// Restart drops the record first, so it is the only form that
		// actually brings a dead daemon back.
		err = t.Restart(actx)
	}

	now := w.now()
	w.mu.Lock()
	defer w.mu.Unlock()
	st.unhealthySince = time.Time{}
	if err != nil {
		st.ConsecutiveFailures++
		st.LastError = err.Error()
		if st.ConsecutiveFailures >= watchGiveUpAfter {
			st.GaveUp = true
			st.NextAttemptMS = 0
			st.GaveUpReason = fmt.Sprintf("%d starts in a row failed; the last error was: %s", st.ConsecutiveFailures, err.Error())
			logger.Error().Int("failures", st.ConsecutiveFailures).Msg("agentmemory: watchdog gave up")
			return
		}
		st.NextAttemptMS = now.Add(backoffFor(st.ConsecutiveFailures)).UnixMilli()
		logger.Warn().Err(err).Int("failures", st.ConsecutiveFailures).Msg("agentmemory: watchdog start failed")
		return
	}

	st.Restarts++
	if reason == ReasonHung {
		st.HungRestarts++
	}
	st.LastReason = reason
	st.LastRestartMS = now.UnixMilli()
	st.LastError = ""
	st.ConsecutiveFailures = 0
	st.NextAttemptMS = 0
	logger.Info().Str("reason", reason).Int("restarts", st.Restarts).Msg("agentmemory: watchdog restarted the daemon")
}

// backoffFor is watchBackoffMin doubled per consecutive failure, capped.
func backoffFor(failures int) time.Duration {
	d := watchBackoffMin
	for i := 1; i < failures; i++ {
		d *= 2
		if d >= watchBackoffMax {
			return watchBackoffMax
		}
	}
	return d
}

// state returns (and creates) the record for one backend.
func (w *Watchdog) state(id string) *WatchdogState {
	w.mu.Lock()
	defer w.mu.Unlock()
	st, ok := w.states[id]
	if !ok {
		st = &WatchdogState{}
		w.states[id] = st
	}
	return st
}

// State is a copy of one backend's record, for the panel payload. The
// unexported timer does not travel with it.
func (w *Watchdog) State(id string) WatchdogState {
	st := w.state(id)
	w.mu.Lock()
	defer w.mu.Unlock()
	out := *st
	out.unhealthySince = time.Time{}
	return out
}

// Start runs the supervision loop until Stop. Safe to call twice: the second
// call is a no-op rather than a second loop over the same managers.
func (w *Watchdog) Start() {
	w.mu.Lock()
	if w.stop != nil {
		w.mu.Unlock()
		return
	}
	stop, done := make(chan struct{}), make(chan struct{})
	w.stop, w.done = stop, done
	w.mu.Unlock()

	go func() {
		defer close(done)
		t := time.NewTicker(watchInterval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				w.Tick(context.Background())
			}
		}
	}()
}

// Stop ends the loop and waits for the current pass to finish.
func (w *Watchdog) Stop() {
	w.mu.Lock()
	stop, done := w.stop, w.done
	w.stop, w.done = nil, nil
	w.mu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	<-done
}

// ── the process-wide watchdog ────────────────────────────────────────

var (
	watchdogMu sync.Mutex
	watchdog   *Watchdog
)

// StartWatchdog starts supervising the registered backends. Called at boot,
// after the config store is wired — the supervision signal is read from it.
// Idempotent.
func StartWatchdog() {
	watchdogMu.Lock()
	defer watchdogMu.Unlock()
	if watchdog == nil {
		watchdog = NewWatchdog()
	}
	watchdog.Start()
}

// StopWatchdog ends supervision. Called on shutdown, before the daemons are
// stopped, so the watchdog cannot race the shutdown by restarting one.
func StopWatchdog() {
	watchdogMu.Lock()
	w := watchdog
	watchdogMu.Unlock()
	if w != nil {
		w.Stop()
	}
}

// WatchdogStateFor is one backend's supervision record for the panel.
//
// Watching and StoppedByOperator are answered live rather than read from the
// last pass: before the first tick — and in a process where the loop never
// started at all — the record is empty, and an empty record rendered as
// "not watching" would be a claim the panel cannot back up. Both come from
// the same functions the loop itself uses.
func WatchdogStateFor(id string) WatchdogState {
	watchdogMu.Lock()
	w := watchdog
	watchdogMu.Unlock()

	var st WatchdogState
	if w != nil {
		st = w.State(id)
	}
	if be, ok := Get(id); ok {
		t := backendTarget{be: be}
		st.Watching = t.Supervised()
		st.StoppedByOperator = t.OperatorStopped()
	}
	return st
}
