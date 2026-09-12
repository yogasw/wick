// Package upgrade implements zero-downtime restarts for the wick daemon:
// a new process starts, inherits the listening socket from the old one, and
// the old process keeps running until every in-flight agent turn has
// finished. Nothing is killed, no request is refused, and no inbound Slack
// event is dropped in between.
//
// Shape of a handoff (parent = old binary, child = new binary):
//
//  1. Operator drops a new binary in place and sends SIGHUP (directly, via
//     `systemctl reload`, or via the tray).
//  2. Parent forks the child, handing over the listener file descriptor.
//  3. Child boots, restores its registry, starts serving HTTP on the SAME
//     socket, then reports Ready.
//  4. Parent stops serving HTTP (the child now answers every new request) and
//     begins DRAINING: it keeps its channel listeners and its agent pool
//     alive so running turns finish and their replies still land in Slack.
//  5. When the pool is idle the parent releases the intake baton and exits.
//     The child, which has been parked waiting for that baton, starts its own
//     channel listeners, cron and schedule runner.
//
// The baton in steps 4–5 is why inbound events are never lost: intake stays
// with the process that owns the running sessions until it is completely
// done, instead of being handed over in a window where neither process is
// listening. See IntakeBaton.
//
// Platform + deployment support:
//
//   - Linux / macOS / BSD, run as a plain daemon or under systemd: full
//     graceful upgrade. Under systemd the child announces itself with
//     sd_notify MAINPID (needs Type=notify + NotifyAccess=all) so the unit
//     follows the new process instead of treating the parent's exit as a
//     crash.
//   - Windows, or wick running under the tray's in-process supervisor
//     (`support-tools status` / processctl managed mode): descriptor passing
//     is not available, so the upgrader degrades to a plain listener and the
//     old stop/start path is used. Everything here stays callable — the
//     methods simply report "not supported", so no caller needs a build tag.
package upgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
)

// ErrUnsupported is returned by Upgrade when this build or this platform
// cannot hand a listener to a new process.
var ErrUnsupported = errors.New("graceful upgrade not supported here")

// forced is set by a HUMAN who has decided not to wait for the drain — the
// Force swap control in the UI. It is the only thing that cuts running work
// short on the default configuration, and it is deliberately not something a
// timer can set: the operator is shown what is still running and chooses.
var forced atomic.Bool

// ForceDrain tells the next (or current) drain to stop waiting. Set it BEFORE
// triggering the handover: once a process is draining it no longer answers
// HTTP, so nothing can reach it to change its mind.
func ForceDrain() { forced.Store(true) }

// Forced reports whether a human asked for the wait to be cut short.
func Forced() bool { return forced.Load() }

// ClearForce resets the flag. Used by tests, and after a handover attempt
// that never started, so a stale force cannot ambush the next upgrade.
func ClearForce() { forced.Store(false) }

// inherited records that this process took over from a predecessor — the one
// fact that proves a handover SUCCEEDED, read after the fact by a UI that
// wants to say so instead of leaving the operator refreshing the page.
var inherited atomic.Bool

// startedAt is when this process began serving, so "took over 20s ago" can be
// distinguished from "has been running since Tuesday".
var startedAt atomic.Int64

// lastHandover remembers the outcome of the most recent attempt to start a
// successor FROM this process. A failed attempt is invisible otherwise: the
// old process simply keeps serving, which looks exactly like nothing having
// been tried.
var lastHandover atomic.Value // handoverResult

type handoverResult struct {
	At    time.Time `json:"at"`
	OK    bool      `json:"ok"`
	Error string    `json:"error,omitempty"`
}

// MarkServing records that this process now answers on the socket, and
// whether it got there by taking over from a predecessor.
func MarkServing(fromHandover bool) {
	inherited.Store(fromHandover)
	startedAt.Store(time.Now().Unix())
}

// Inherited reports whether this process took over from a predecessor.
func Inherited() bool { return inherited.Load() }

// ServingSince reports when this process started serving (zero if unknown).
func ServingSince() time.Time {
	if v := startedAt.Load(); v > 0 {
		return time.Unix(v, 0)
	}
	return time.Time{}
}

// LastHandover returns the outcome of the last successor this process tried
// to start: zero time when it has never tried.
func LastHandover() (at time.Time, ok bool, errMsg string) {
	v, _ := lastHandover.Load().(handoverResult)
	return v.At, v.OK, v.Error
}

// handoverStateFile is where the outcome of the last attempt is left for a
// process that is not this one. `reload` polls for a new pid and has no way to
// hear "I refused to start a successor" — so it waited out its whole timeout
// for a handover that was never going to happen. Five minutes of an operator's
// time, for something the daemon knew instantly.
const handoverStateFile = "handover-last.json"

func recordHandover(err error) {
	r := handoverResult{At: time.Now(), OK: err == nil}
	if err != nil {
		r.Error = err.Error()
	}
	lastHandover.Store(r)
	writeHandoverState(r)
}

// writeHandoverState persists the attempt. Best-effort: a missing file only
// costs the CLI its early exit.
func writeHandoverState(r handoverResult) {
	dir := stateDir.Load()
	if dir == nil || *dir == "" {
		return
	}
	b, err := json.Marshal(r)
	if err != nil {
		return
	}
	//nolint:errcheck // see doc comment
	_ = os.WriteFile(filepath.Join(*dir, handoverStateFile), b, 0o644)
}

// stateDir is the data dir, remembered so package-level helpers can write
// beside the pid file without every caller threading it through.
var stateDir atomic.Pointer[string]

// HandoverStatePath is where the last attempt is recorded. Exported so the
// CLI reads the same path the daemon writes.
func HandoverStatePath(baseDir string) string {
	return filepath.Join(baseDir, handoverStateFile)
}

// AutoSwap is what the binary watcher is doing right now, published so the UI
// can show it. Without this the watcher is invisible: a build sits on disk,
// nothing appears to happen for up to two check intervals, and an operator
// reasonably concludes it is stuck.
type AutoSwap struct {
	// State: "" (nothing waiting), "settling" (a new file was just seen and
	// has to hold still before it is trusted), "waiting" (stable, but work is
	// in flight), "previous_draining" (an earlier generation has not exited,
	// so only two processes may exist at once), "handing_over" (the successor
	// has been started).
	State string `json:"state"`
	To    string `json:"to,omitempty"`
	// NextCheckIn is seconds until the next check, so a countdown can run.
	NextCheckIn int `json:"next_check_in,omitempty"`
	// ElapsedSeconds is how long this state has been in force. Counted on the
	// SERVER, because a page refresh must not restart the clock on a handover
	// that has been underway for a minute.
	ElapsedSeconds int `json:"elapsed_seconds,omitempty"`
	// GivesUpInSeconds is how long the predecessor waits for a successor
	// before giving up and carrying on — the honest answer to "until when?".
	GivesUpInSeconds int `json:"gives_up_in_seconds,omitempty"`

	since time.Time
}

var autoSwap atomic.Value // AutoSwap

// SetAutoSwap publishes the watcher's current state. Entering a DIFFERENT
// state restarts its clock; repeating the same one does not, so the elapsed
// time survives both the 15s tick and a browser refresh.
func SetAutoSwap(s AutoSwap) {
	prev, _ := autoSwap.Load().(AutoSwap)
	if prev.State == s.State && !prev.since.IsZero() {
		s.since = prev.since
	} else {
		s.since = time.Now()
	}
	autoSwap.Store(s)
}

// HandoverTimeout is how long a process waits for its successor to report
// ready before giving up on it and continuing to serve.
func HandoverTimeout() time.Duration { return upgradeTimeout }

// processStart is when this process began. Used to measure how long a boot
// actually takes here, which is the only honest basis for telling an operator
// how much longer a handover has to go.
var processStart = time.Now()

// typicalBoot is the last measured boot, in seconds. Zero until one has been
// recorded on this host.
var typicalBoot atomic.Int64

const bootStatsFile = "last-boot.json"

// TypicalBootSeconds is how long the previous boot took here — an estimate
// grounded in this host's own history rather than a guess. 0 when unknown.
func TypicalBootSeconds() int { return int(typicalBoot.Load()) }

// LoadBootStats reads the recorded boot duration. Call once at startup.
func LoadBootStats(dir string) {
	if dir == "" {
		return
	}
	b, err := os.ReadFile(filepath.Join(dir, bootStatsFile))
	if err != nil {
		return
	}
	var v struct {
		Seconds int64 `json:"seconds"`
	}
	if json.Unmarshal(b, &v) == nil && v.Seconds > 0 {
		typicalBoot.Store(v.Seconds)
	}
}

// RecordBootComplete stores how long THIS process took to become ready, so
// the next handover can say "usually about this long" instead of leaving the
// operator guessing whether a minute of silence is normal.
func RecordBootComplete(dir string) {
	secs := int64(time.Since(processStart).Seconds())
	if secs <= 0 {
		secs = 1
	}
	typicalBoot.Store(secs)
	if dir == "" {
		return
	}
	b, err := json.Marshal(struct {
		Seconds int64     `json:"seconds"`
		At      time.Time `json:"at"`
	}{secs, time.Now()})
	if err != nil {
		return
	}
	//nolint:errcheck // a missing stat file only costs the next estimate
	_ = os.WriteFile(filepath.Join(dir, bootStatsFile), b, 0o644)
}

// AutoSwapStatus reports the watcher's last published state, with the elapsed
// and remaining seconds filled in as of now.
func AutoSwapStatus() AutoSwap {
	v, _ := autoSwap.Load().(AutoSwap)
	if v.State == "" || v.since.IsZero() {
		return v
	}
	elapsed := time.Since(v.since)
	v.ElapsedSeconds = int(elapsed.Seconds())
	if v.State == "handing_over" {
		if left := HandoverTimeout() - elapsed; left > 0 {
			v.GivesUpInSeconds = int(left.Seconds())
		}
	}
	return v
}

// draining marks a process that has already handed its socket over and is
// finishing its work. It stops the binary watcher: a successor is serving
// now, and it is the one that should decide about any newer build. Two
// generations both trying to start a third is a fight nobody wins.
var draining atomic.Bool

// MarkDraining records that this process has been superseded.
func MarkDraining() { draining.Store(true) }

// Draining reports whether this process is on its way out.
func Draining() bool { return draining.Load() }

// armed mirrors whether THIS process can hand its socket to a successor, for
// callers that only need the answer (the admin UI) and have no reason to hold
// the Upgrader itself.
var armed atomic.Bool

// MarkArmed records whether graceful upgrade is available in this process.
func MarkArmed(v bool) { armed.Store(v) }

// Armed reports what MarkArmed recorded: true when a reload hands over, false
// when it would be a stop/start.
func Armed() bool { return armed.Load() }

// flipper is the descriptor-passing engine, implemented by tableflip on
// unix and by nothing at all on Windows.
type flipper interface {
	Listen(network, addr string) (net.Listener, error)
	Ready() error
	Exit() <-chan struct{}
	Upgrade() error
	Stop()
	HasParent() bool
}

// Upgrader owns the listener and the handoff. A nil-safe zero value behaves
// exactly like the "unsupported" case, so callers can hold one
// unconditionally.
type Upgrader struct {
	flip    flipper
	baseDir string
	never   chan struct{} // Exit() when disabled: never closes
}

// upgradeTimeout bounds how long a parent waits for its successor to report
// ready. Generous on purpose: the child has to restore the whole registry
// before it can serve, and a host under agent load is slow.
const upgradeTimeout = 3 * time.Minute

// Wanted reports whether the operator asked for graceful upgrades.
//
// Opt-in (WICK_GRACEFUL_UPGRADE=1) rather than default-on: it changes how the
// process exits, and a deployment that never sends SIGHUP gains nothing from
// it. Set it in the unit / launchd plist once the handoff has been verified on
// that host.
func Wanted() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("WICK_GRACEFUL_UPGRADE")))
	return v == "1" || v == "true" || v == "yes"
}

// New builds an Upgrader. It never fails the caller: when graceful upgrade is
// unavailable — unsupported platform, not requested, or descriptor passing
// refused — it returns a working Upgrader that simply listens normally, plus
// the reason, so the daemon boots either way.
func New(baseDir string) (*Upgrader, string) {
	u := &Upgrader{baseDir: baseDir, never: make(chan struct{})}
	if baseDir != "" {
		d := baseDir
		stateDir.Store(&d)
	}
	if !Wanted() {
		return u, "disabled (set WICK_GRACEFUL_UPGRADE=1 to enable)"
	}
	if !supported() {
		return u, "unsupported on this platform"
	}
	pidFile := ""
	if baseDir != "" {
		pidFile = baseDir + string(os.PathSeparator) + "wick.pid"
	}
	f, err := newFlipper(pidFile)
	if err != nil {
		return u, fmt.Sprintf("unavailable: %v", err)
	}
	u.flip = f
	return u, ""
}

// Enabled reports whether this process can actually hand off.
func (u *Upgrader) Enabled() bool { return u != nil && u.flip != nil }

// IsChild reports whether this process was started BY an upgrade — i.e. it
// inherited its listener from a parent that is still draining.
func (u *Upgrader) IsChild() bool { return u.Enabled() && u.flip.HasParent() }

// Listen returns the listening socket: inherited from the parent during an
// upgrade, freshly bound otherwise.
func (u *Upgrader) Listen(network, addr string) (net.Listener, error) {
	if !u.Enabled() {
		return net.Listen(network, addr)
	}
	return u.flip.Listen(network, addr)
}

// NotifyServing tells the SERVICE MANAGER that this process is now the one
// answering on the socket. Call it the moment the serve loop starts.
//
// Split from Ready on purpose. systemd's Type=notify has a start timeout, and
// wick's boot gate can legitimately take minutes to restore a big registry —
// waiting for that before the first READY=1 would get a healthy daemon killed
// as a failed start. The MAINPID in here is also what makes an upgrade
// survivable: after a handoff the unit must follow the child, or the parent's
// exit looks like the service dying and systemd kills the whole cgroup.
//
// No-op when NOTIFY_SOCKET is unset, i.e. everywhere except systemd.
func (u *Upgrader) NotifyServing() {
	if err := NotifyReady(); err != nil {
		log.Debug().Err(err).Msg("upgrade: sd_notify READY failed")
	}
	_ = NotifyStatus("serving")
}

// Ready announces that this process is fully warmed up. It closes the
// PARENT's Exit channel, which is what starts the drain over there — so call
// it only once this process can genuinely do the work, not merely accept
// connections.
func (u *Upgrader) Ready() {
	if u.Enabled() {
		if err := u.flip.Ready(); err != nil {
			log.Warn().Err(err).Msg("upgrade: signalling readiness failed")
		}
	}
	_ = NotifyStatus("ready")
}

// Exit closes when a successor has taken over and this process should drain
// and go away. Never closes when graceful upgrade is off.
func (u *Upgrader) Exit() <-chan struct{} {
	if !u.Enabled() {
		return u.never
	}
	return u.flip.Exit()
}

// SpawnWrapper wraps the fork of a successor process. wick sets it to
// logfiles.WithOriginalStdio so the child inherits the real stdio instead of
// this process's log pipes — inheriting a pipe whose reader dies with the
// parent kills the successor with SIGPIPE moments after a clean handoff.
//
// A package var rather than a constructor argument: the upgrader is built deep
// inside server startup, while the log plumbing is set up in main.
var SpawnWrapper func(func() error) error

// trigger starts a successor. Registered by the server so callers that have
// no business holding the Upgrader — the admin UI — can still ask for a
// handover, which is otherwise only reachable by sending this process
// SIGHUP from a shell.
var trigger atomic.Value // func() error

// SetTrigger registers how a handover is started in this process.
func SetTrigger(fn func() error) {
	if fn != nil {
		trigger.Store(fn)
	}
}

// Trigger starts a successor, exactly as SIGHUP would. ErrUnsupported when
// graceful upgrade is not available here.
func Trigger() error {
	fn, _ := trigger.Load().(func() error)
	if fn == nil {
		return ErrUnsupported
	}
	return fn()
}

// BeforeSpawn runs in this process immediately before a successor is forked.
// It is where state that only lives in memory is handed across — the scoped
// MCP tokens the running agents authenticate with, which the successor would
// otherwise reject with a 401 the moment it owns the socket.
//
// A package var rather than a constructor argument, for the same reason as
// SpawnWrapper: the upgrader is built deep inside server startup, while what
// it needs to persist is owned elsewhere.
var BeforeSpawn func()

// Upgrade starts a successor process. Returns ErrUnsupported when this build
// cannot.
func (u *Upgrader) Upgrade() error {
	if !u.Enabled() {
		return ErrUnsupported
	}
	if BeforeSpawn != nil {
		BeforeSpawn()
	}
	if SpawnWrapper != nil {
		return SpawnWrapper(u.flip.Upgrade)
	}
	return u.flip.Upgrade()
}

// Stop releases the upgrader's own resources. Safe to defer unconditionally.
func (u *Upgrader) Stop() {
	if u.Enabled() {
		u.flip.Stop()
	}
}

// WatchSignal starts a successor whenever SIGHUP arrives — the conventional
// "reload" signal, so `systemctl reload`, `kill -HUP`, and
// `support-tools upgrade` all work through one path. Returns immediately;
// the watcher lives until ctx is done.
//
// SIGHUP is ignored (with a log line) when graceful upgrade is off, which is
// strictly better than the Go default of terminating the process.
func (u *Upgrader) WatchSignal(ctx context.Context) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	go func() {
		defer signal.Stop(ch)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ch:
				if !u.Enabled() {
					log.Warn().Msg("upgrade: SIGHUP received but graceful upgrade is off — ignoring (restart the service instead)")
					continue
				}
				log.Info().Msg("upgrade: SIGHUP received — starting successor process")
				_ = NotifyStatus("starting successor process")
				if err := u.Upgrade(); err != nil {
					recordHandover(err)
					ev := log.Error().Err(err)
					if strings.Contains(err.Error(), "parent hasn't exited") {
						// At most two generations may exist at once. This
						// process is still the CHILD of a predecessor that is
						// draining (typically waiting on a long agent turn),
						// so it cannot fork a third. Not a failure of this
						// upgrade — a "not yet".
						ev = ev.Str("hint", "the previous generation is still draining; retry once it has exited")
					}
					ev.Msg("upgrade: successor not started — this process keeps serving")
					// Do NOT leave the failure as the unit's status: it would
					// sit in `systemctl status` forever and read as a broken
					// service, when the service is serving fine.
					_ = NotifyStatus("ready")
					continue
				}
				recordHandover(nil)
				log.Info().Msg("upgrade: successor is ready")
			}
		}
	}()
}

// AcquireIntake blocks until this process owns the intake baton (see
// IntakeBaton) or ctx is done. It is safe on every platform: where flock is
// unavailable the baton is a no-op and returns immediately.
func (u *Upgrader) AcquireIntake(ctx context.Context) (*IntakeBaton, error) {
	return acquireIntake(ctx, u.baseDir)
}

// DrainTimeout is an OPTIONAL hard ceiling on the whole drain, for an
// unattended deploy that must finish inside a known time. Unset (0) by
// default, because a ceiling here does not stop work — it kills it: a
// workflow run cut off mid-node, a cron job cut off mid-write, an agent
// reply cut off mid-sentence. The drain otherwise waits for the work.
//
// WICK_DRAIN_TIMEOUT sets it (any Go duration).
func DrainTimeout() time.Duration {
	return envDuration("WICK_DRAIN_TIMEOUT", 0)
}

// DrainQuiet is the SETTLE WINDOW the whole drain switches on. It is not a
// deadline on anything: no work is ever interrupted because it expired.
//
// The condition it completes is "this process has stopped", not "enough time
// has passed" — every registered subsystem at zero, and still at zero this
// long afterwards. The window exists because work going quiet for an instant
// is not work ending: a tool result, a queued message, the next node of a
// workflow or a sub-agent reporting back lands moments later, and handing
// over inside that gap cuts off work that had merely paused.
//
// It applies to agent turns, workflow runs, cron jobs, connector and plugin
// calls, delegations and scheduled deliveries alike — one rule, so a
// subsystem added later is covered by registering itself and nothing else.
//
// WICK_DRAIN_QUIET overrides it (any Go duration); WICK_DRAIN_AGENT_QUIET is
// still read for continuity with the agent-only window it replaces.
func DrainQuiet() time.Duration {
	if v := strings.TrimSpace(os.Getenv("WICK_DRAIN_QUIET")); v != "" {
		return envDuration("WICK_DRAIN_QUIET", 15*time.Second)
	}
	return envDuration("WICK_DRAIN_AGENT_QUIET", 15*time.Second)
}

func envDuration(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	if v == "0" {
		return 0
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		log.Warn().Str("key", key).Str("value", v).Msg("upgrade: invalid duration, using default")
		return def
	}
	return d
}
