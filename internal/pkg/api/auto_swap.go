package api

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/yogasw/wick/internal/pkg/daemon"
	"github.com/yogasw/wick/internal/pkg/upgrade"
)

// autoSwapInterval is how often the exec path is checked. Also the stability
// window: a binary must look identical across two consecutive checks before
// it is handed over to, so a copy still in flight is never executed.
const autoSwapInterval = 15 * time.Second

// watchBinarySwap hands over to a binary that was installed at this process's
// exec path but never applied — the state a deploy script leaves behind when
// it copies a file and stops there.
//
// Installing a binary tells the daemon nothing: it does not watch its own
// file, so without this the new build simply sits there while the old process
// keeps serving, and the version an operator believes they deployed is not the
// one answering. That gap was only visible if somebody thought to look.
//
// Three conditions, all of them about safety rather than timing:
//
//   - the file differs from the running image (version or build timestamp),
//   - it looked exactly the same one interval ago — size and mtime — so a
//     half-written file is never executed,
//   - nothing UNRESUMABLE is in flight — a workflow run mid-node, a cron job
//     mid-write, a connector call. Those are the ones a handover could strand,
//     and they are short.
//
// Agent turns deliberately do NOT hold it back. The handover does not touch
// them: the old process keeps serving its turns to the end and only then
// exits, so a turn is no more interrupted by swapping than by not swapping.
// Waiting for them was a mistake that only showed on a busy host — with two
// conversations replying in turn, "nothing in flight" is a moment that may
// never arrive, and the swap sat in "waiting for idle" for minutes while the
// dashboard insisted something was happening. Two generations alive for the
// length of a long turn is the price, and it is the price this whole feature
// already pays during the drain.
//
// A file that fails to take over is not retried. Otherwise a broken build
// would be handed over to every 15 seconds, forever.
func (s *Server) watchBinarySwap(ctx context.Context, runningVersion, runningBuiltAt string, logger *zerolog.Logger) {
	ticker := time.NewTicker(autoSwapInterval)
	defer ticker.Stop()

	var sw autoSwapper

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if !upgrade.Armed() || upgrade.Draining() {
			// Either no handover is possible here, or this process has already
			// been superseded and the successor owns the decision.
			return
		}
		p, ok := sw.inspect(runningVersion, runningBuiltAt)
		// Only work that CANNOT be resumed gates the trigger; see the
		// comment on watchBinarySwap. Busy() is still what gets logged, so
		// the line says everything that was running, not just the blockers.
		busy := upgrade.BusyKind(false)
		fire := sw.shouldSwap(p, ok, busy)
		// Publish what this tick concluded BEFORE acting on it, so a page open
		// during the wait shows the reason rather than an unexplained pause.
		upgrade.SetAutoSwap(sw.status(p, ok, busy, fire))
		if !fire {
			if ok && len(busy) > 0 {
				logger.Debug().Strs("blocking", busy).Strs("running", upgrade.Busy()).Str("to", p.Version).
					Msg("auto-swap: a new binary is installed, waiting for work that cannot be resumed")
			}
			continue
		}
		logger.Info().Str("from", runningVersion).Str("to", p.Version).Str("path", p.Path).
			Strs("still_running", upgrade.Busy()).
			Msg("auto-swap: a new binary is installed — handing over (running turns finish in this process)")
		if err := upgrade.Trigger(); err != nil {
			upgrade.SetAutoSwap(upgrade.AutoSwap{})
			// "parent hasn't exited" means a PREVIOUS generation is still
			// draining — at most two may exist at once. That is a "not yet",
			// not a bad binary, and blacklisting the file for it would mean
			// this build is never applied at all.
			if strings.Contains(err.Error(), "parent hasn't exited") {
				// Say so on screen too: a blank phase here reads as "it gave
				// up", when it is the queue working exactly as designed.
				upgrade.SetAutoSwap(upgrade.AutoSwap{
					State:       "previous_draining",
					To:          p.Version,
					NextCheckIn: int(autoSwapInterval / time.Second),
				})
				logger.Info().Str("to", p.Version).
					Msg("auto-swap: previous generation still draining — will try again")
				continue
			}
			sw.markFailed(p)
			logger.Warn().Err(err).Str("to", p.Version).
				Msg("auto-swap: handover did not start — this process keeps serving, and this file will not be retried")
			continue
		}
		// The successor is on its way; this process is now draining and its
		// job here is done.
		upgrade.SetAutoSwap(upgrade.AutoSwap{State: "handing_over", To: p.Version})
		return
	}
}

// autoSwapper holds the two things the decision needs to remember between
// checks: what the previous check saw (so a file can be required to hold
// still), and which file has already been tried and failed (so a broken build
// is not handed over to every 15 seconds, forever).
type autoSwapper struct {
	seen   daemon.Pending
	failed daemon.Pending

	// Cache of the last inspection, keyed by the cheap file identity. Parsing
	// a binary's build info means opening and seeking an 88 MB file; doing
	// that every 15 seconds forever, on a file that changes once a month, is
	// waste. A stat is enough to know nothing happened.
	statSize int64
	statMod  int64
	statOK   bool
	cached   daemon.Pending
	cachedOK bool
}

// inspect answers PendingSwap, but only re-reads the binary when its size or
// mtime has moved since the last check.
func (a *autoSwapper) inspect(runningVersion, runningBuiltAt string) (daemon.Pending, bool) {
	path := daemon.ServingBinaryPath()
	if path == "" {
		return daemon.Pending{}, false
	}
	st, err := os.Stat(path)
	if err != nil {
		a.statOK = false
		return daemon.Pending{}, false
	}
	if a.statOK && st.Size() == a.statSize && st.ModTime().Unix() == a.statMod {
		return a.cached, a.cachedOK
	}
	a.statSize, a.statMod, a.statOK = st.Size(), st.ModTime().Unix(), true
	a.cached, a.cachedOK = daemon.PendingSwap(runningVersion, runningBuiltAt)
	return a.cached, a.cachedOK
}

// shouldSwap answers the only question the watcher asks. Kept pure so the
// conditions guarding an automatic replacement of the running process can be
// tested without a clock, a filesystem, or a live daemon.
func (a *autoSwapper) shouldSwap(p daemon.Pending, pending bool, busy []string) bool {
	if !pending {
		a.seen = daemon.Pending{} // nothing waiting; forget what we saw
		return false
	}
	if a.failed.Path != "" && samePendingFile(p, a.failed) {
		return false // already tried this exact file and it did not take over
	}
	if !samePendingFile(p, a.seen) {
		a.seen = p // first sighting, or still being written — wait one more tick
		return false
	}
	// busy here is the UNRESUMABLE work only (see watchBinarySwap): those are
	// the calls a handover could strand. Agent turns finish in the outgoing
	// process either way, so they are not consulted.
	return len(busy) == 0
}

func (a *autoSwapper) markFailed(p daemon.Pending) { a.failed = p }

// status turns the same inputs the decision used into the line the UI shows.
// Derived from them rather than set alongside them, so the two can never
// disagree about why nothing is happening.
func (a *autoSwapper) status(p daemon.Pending, pending bool, busy []string, fire bool) upgrade.AutoSwap {
	switch {
	case !pending:
		return upgrade.AutoSwap{}
	case a.failed.Path != "" && samePendingFile(p, a.failed):
		// Tried, did not take over, will not be retried — the banner says so
		// via last_handover; here it is simply not counting down.
		return upgrade.AutoSwap{State: "waiting", To: p.Version}
	case fire:
		return upgrade.AutoSwap{State: "handing_over", To: p.Version}
	case len(busy) > 0:
		return upgrade.AutoSwap{State: "waiting", To: p.Version, NextCheckIn: int(autoSwapInterval / time.Second)}
	default:
		return upgrade.AutoSwap{State: "settling", To: p.Version, NextCheckIn: int(autoSwapInterval / time.Second)}
	}
}

// samePendingFile compares identity, not just path: a rebuild of the same
// version at the same path is a different file, and must not be mistaken for
// the one already seen.
func samePendingFile(a, b daemon.Pending) bool {
	return a.Path == b.Path && a.Size == b.Size && a.ModTime == b.ModTime && a.Version == b.Version
}
