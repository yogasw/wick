package sessionworkspace

import (
	"os"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
)

// IdleGrace is how long a session may sit idle (no running/queued subprocess,
// no activity) before its connector instances are reaped. Instances are
// throwaway credential clones that exist only for active work on a session;
// once the session goes quiet, holding their config (secrets and all) is pure
// exposure with no benefit, so we drop them. A short grace lets a user step
// away from a chat and come back without losing their setup. Tune here.
const IdleGrace = 10 * time.Minute

// sweepInterval is how often the reaper checks for idle sessions. The grace
// window is measured in minutes, so a minute of slack is invisible; a tighter
// tick would just re-read session metadata more often for no benefit.
const sweepInterval = time.Minute

// The sweeper is a self-terminating goroutine: it starts when an instance is
// added (ensureSweeper) and stops itself once a scan finds no instances left
// anywhere, so an idle server runs no reaper at all. sweepMu guards the
// running flag so only one goroutine exists at a time.
var (
	sweepMu      sync.Mutex
	sweepRunning bool
)

// reapNotify, if set, is called once per session right after its connectors
// are reaped, with the tombstones just written. The server wires this to
// notify the session's agent (a silent turn) that its connectors are gone so
// its next reply has the context. nil = no notification (e.g. in tests).
var reapNotify func(sessionID string, tombs []Tombstone)

// SetReapNotify installs the post-reap notification hook. Call once at boot,
// before StartSweeper. Passing nil disables notification.
func SetReapNotify(fn func(sessionID string, tombs []Tombstone)) {
	reapNotify = fn
}

// ensureSweeper starts the reaper goroutine if it isn't already running. Safe
// to call on every Add — a no-op when one is live. Idempotent and cheap.
func ensureSweeper(layout agentconfig.Layout) {
	sweepMu.Lock()
	defer sweepMu.Unlock()
	if sweepRunning {
		return
	}
	sweepRunning = true
	go runSweeper(layout)
}

// StartSweeper does a single scan at boot and, if any instances remain, leaves
// the reaper running. Called once from the web server so a process restart
// still reaps instances whose sessions went idle while it was down. When every
// workspace is already empty it spawns nothing.
func StartSweeper(layout agentconfig.Layout) {
	remaining, err := sweepOnce(layout, time.Now())
	if err != nil {
		log.Warn().Err(err).Msg("session workspace sweeper: boot scan failed")
		return
	}
	if remaining > 0 {
		ensureSweeper(layout)
	}
}

// runSweeper ticks until a scan reports zero instances remaining, then clears
// the running flag and exits. A later Add calls ensureSweeper again to respawn.
func runSweeper(layout agentconfig.Layout) {
	l := log.With().Str("component", "session-workspace-sweeper").Logger()
	l.Debug().Dur("interval", sweepInterval).Dur("idle_grace", IdleGrace).Msg("started")
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for range ticker.C {
		remaining, err := sweepOnce(layout, time.Now())
		if err != nil {
			l.Warn().Err(err).Msg("sweep failed")
			continue // transient FS error — keep ticking, don't die on it
		}
		if remaining == 0 {
			break
		}
	}
	sweepMu.Lock()
	sweepRunning = false
	sweepMu.Unlock()
	l.Debug().Msg("stopped (no instances left)")
}

// sweepOnce scans every session workspace once, reaping the instances of any
// session that has gone idle past IdleGrace, and returns how many LIVE
// instances remain across all sessions (in still-active sessions). The reaper
// uses that count to decide whether to keep ticking.
func sweepOnce(layout agentconfig.Layout, now time.Time) (int, error) {
	entries, err := os.ReadDir(layout.SessionsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	remaining := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sid := e.Name()
		ws, err := Load(layout, sid)
		if err != nil || len(ws.Instances) == 0 {
			continue // unreadable, or nothing live to reap (tombstones-only is fine)
		}
		if sessionIdle(layout, sid, now) {
			if n, rerr := reapAll(layout, sid, "session idle", now); rerr != nil {
				log.Warn().Err(rerr).Str("session_id", sid).Msg("session workspace sweeper: reap failed")
			} else if n > 0 {
				log.Info().Str("session_id", sid).Int("reaped", n).Msg("session workspace: reaped idle session's connectors")
				// Tell the agent its connectors are gone (silent turn). Fetch
				// the tombstones just written so the message can name them.
				if reapNotify != nil {
					if tombs, terr := Tombstones(layout, sid); terr == nil && len(tombs) > 0 {
						reapNotify(sid, tombs)
					}
				}
			}
			continue
		}
		remaining += len(ws.Instances)
	}
	return remaining, nil
}

// sessionIdle reports whether a session is quiet enough to reap: it must not
// have a live subprocess (Status idle) AND its last activity must be older than
// IdleGrace. A running/queued session is never reaped, so instances stay alive
// as long as work is happening. A session whose meta.json is gone (deleted) is
// treated as idle — its instances should not outlive it.
func sessionIdle(layout agentconfig.Layout, sessionID string, now time.Time) bool {
	sess, err := session.Load(layout, sessionID)
	if err != nil {
		return true // no/unreadable meta → session is gone; reap its instances
	}
	if sess.Meta.Status != session.StatusIdle {
		return false // running or queued — active work, keep alive
	}
	return now.Sub(sess.Meta.LastActive) > IdleGrace
}
