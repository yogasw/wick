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
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/rs/zerolog/log"
)

// ErrUnsupported is returned by Upgrade when this build or this platform
// cannot hand a listener to a new process.
var ErrUnsupported = errors.New("graceful upgrade not supported here")

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

// Upgrade starts a successor process. Returns ErrUnsupported when this build
// cannot.
func (u *Upgrader) Upgrade() error {
	if !u.Enabled() {
		return ErrUnsupported
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

// DrainTimeout caps how long a draining parent waits for work that CANNOT be
// resumed — a workflow run mid-node, a cron job mid-write, a connector call.
// Minutes are normal for those, so the default is generous; WICK_DRAIN_TIMEOUT
// overrides it (any Go duration).
func DrainTimeout() time.Duration {
	return envDuration("WICK_DRAIN_TIMEOUT", 20*time.Minute)
}

// AgentGrace caps how long a draining parent waits for RESUMABLE work —
// agent turns. Short by default, and that is a deliberate trade:
//
// An interactive Slack session is "in flight" for as long as somebody keeps
// talking to it, so waiting for it kept the old process alive for hours. Two
// processes at once is visible and confusing (only one holds intake, so the
// browser talks to one while the agents run in the other), and it also blocks
// the NEXT upgrade — tableflip refuses while a parent is still alive.
//
// A turn interrupted here is not lost work: the session lives on disk and the
// next message resumes it. Set WICK_DRAIN_AGENT_GRACE higher (e.g. 20m) to
// let long debug runs finish instead, accepting the longer overlap; set 0 to
// hand over immediately.
func AgentGrace() time.Duration {
	return envDuration("WICK_DRAIN_AGENT_GRACE", 45*time.Second)
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
