package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yogasw/wick/pkg/connector"
)

// reaper.go terminates live browsers that nobody is using any more.
//
// Why this exists: a live session is a DETACHED Chromium (see livesession.go) —
// it deliberately outlives the plugin subprocess, which wick kills after ~5min
// idle. Nothing else ends it: session_close is the only exit, and it depends on
// the caller remembering to call it. An agent that crashes, times out, or simply
// forgets leaves a browser resident forever. Measured on a live host: one idle
// instance held ~180M of real (cgroup-attributed) memory indefinitely, and its
// renderer kept growing while nothing touched it.
//
// The reaper closes that gap by measuring sessionMeta.LastUsed, which every op
// refreshes via touchSession.
//
// Scheduling: there is no long-lived process to hold a timer — the plugin is
// killed every few minutes. So the sweep runs opportunistically, whenever the
// plugin is awake and handling a live-session op (see maybeReap). That covers
// the real case: an abandoned browser is reclaimed the next time anything
// browser-related happens. A totally idle host never sweeps, which is harmless —
// if nothing is running, nothing is competing for the memory either.

const (
	// defaultIdleTimeout is how long an ANONYMOUS session may sit untouched.
	// Short on purpose: an anonymous session holds no login worth preserving,
	// and on a memory-constrained host an abandoned browser is pure cost.
	defaultIdleTimeout = time.Minute

	// namedProfileFactor multiplies the idle timeout for sessions bound to a
	// named profile. Reaping those costs the user their logged-in state, so
	// they get a much longer leash — but not an infinite one, so nothing lives
	// forever. 8 x the 1m default = 8m.
	namedProfileFactor = 8

	// reapStamp is the marker file recording the last sweep, so a burst of ops
	// does not re-scan the session dir on every single call.
	reapStamp = ".last-reap"

	// reapInterval is the minimum gap between two sweeps. Kept well under the
	// shortest idle timeout so a session is reclaimed near its deadline rather
	// than up to a full interval late.
	reapInterval = 15 * time.Second
)

// idleTimeoutFor resolves the configured idle timeout for a session. Config
// browser_idle_timeout_min is in minutes; 0 (or negative) disables reaping
// entirely, restoring the old "lives until session_close" behaviour.
// A named profile gets the longer namedProfileIdleTimeout unless the admin
// configured an explicit value, in which case that value is scaled up by the
// same ratio so one knob still controls both.
func idleTimeoutFor(c *connector.Ctx, m sessionMeta) time.Duration {
	base := defaultIdleTimeout
	if v := c.CfgInt("browser_idle_timeout_min"); v != 0 {
		if v < 0 {
			return 0 // explicit opt-out
		}
		base = time.Duration(v) * time.Minute
	}
	if m.Profile == "" {
		return base
	}
	// Named profile: one knob still controls both, scaled by a fixed factor.
	return base * namedProfileFactor
}

// sessionIdleFor reports how long a session has gone untouched. Sessions
// written before LastUsed existed have a zero value; those fall back to
// Created so an old file is not treated as idle since the epoch.
func sessionIdleFor(m sessionMeta, now time.Time) time.Duration {
	ref := m.LastUsed
	if ref.IsZero() {
		ref = m.Created
	}
	if ref.IsZero() {
		return 0 // no timestamps at all: refuse to judge it idle
	}
	return now.Sub(ref)
}

// touchSession refreshes LastUsed for a session. Every op that acts on a live
// browser calls this, which is what makes idle measurable. Best-effort: a
// failed write must never fail the op the user actually asked for.
func touchSession(c *connector.Ctx, id string) {
	dir := sessionDir(c)
	m, err := readMeta(dir, id)
	if err != nil {
		return
	}
	m.LastUsed = sessionNow(c)
	_ = writeMeta(dir, m)
}

// reapIdleSessions terminates every session idle past its timeout and returns
// the ids it killed. Termination is the full sequence — kill the PID, then
// remove metadata and (for anonymous sessions) the profile dir — so a reaped
// session never leaves the stale singleton files that would wedge the next
// launch against the same profile.
func reapIdleSessions(c *connector.Ctx) []string {
	dir := sessionDir(c)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	now := sessionNow(c)
	var killed []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".json")
		m, err := readMeta(dir, id)
		if err != nil {
			continue
		}
		timeout := idleTimeoutFor(c, m)
		if timeout <= 0 {
			continue // reaping disabled
		}
		if sessionIdleFor(m, now) < timeout {
			continue
		}
		killPID(m.PID)
		// The profile dir survives for named profiles (removeSession keeps it),
		// but its lock files must go either way — they point at the PID we just
		// killed, and Chrome refuses to reuse a dir still claimed by a stale
		// singleton.
		if m.UserData != "" {
			clearStaleProfileLocks(m.UserData)
		}
		removeSession(dir, m)
		killed = append(killed, id)
	}
	return killed
}

// ownedProc is a running browser process wick owns, identified by the
// user-data dir on its command line. Both platform implementations of
// findOwnedBrowserPIDs return this shape.
type ownedProc struct {
	PID         int
	UserDataDir string
}

// killOrphans reports whether orphan sweeping is enabled (config
// browser_kill_orphans). Off by default: the sweep enumerates every process on
// the host, which suits a server but is needless work on a developer machine.
func killOrphans(c *connector.Ctx) bool { return c.CfgBool("browser_kill_orphans") }

// reapOrphanBrowsers kills browser processes under the session dir that no
// live session claims. It exists because sessionMeta.PID cannot describe a
// browser fully: engines fork children (Chrome's renderer/GPU/utility) that
// appear in no metadata, and some — Firefox especially — can let the recorded
// PID exit while the real browser lives on under a different one. Anything that
// escaped the normal reap is invisible to a PID-based sweep but still holds RAM.
//
// Ownership is decided by --user-data-dir, so a browser the user launched
// themselves can never match. Processes belonging to sessions that are still
// within their idle window are skipped: this reclaims the unclaimed, it does
// not cut short work in progress.
//
// Off by default (browser_kill_orphans). It scans every process on the host, so
// it is meant for servers where a stray browser is pure cost — not for a laptop
// where the same scan runs against a desktop full of unrelated processes.
func reapOrphanBrowsers(c *connector.Ctx) []int {
	root := sessionDir(c)
	procs := findOwnedBrowserPIDs(root)
	if len(procs) == 0 {
		return nil
	}

	// Protect by user-data DIR, not by PID. A live session is a whole process
	// tree sharing one --user-data-dir; protecting only the recorded parent
	// would leave its renderers unprotected and kill them out from under a
	// session that is actively in use.
	protectedDirs := map[string]bool{}
	if metas, err := listSessions(c); err == nil {
		now := sessionNow(c)
		for _, m := range metas {
			if m.UserData == "" {
				continue
			}
			timeout := idleTimeoutFor(c, m)
			if timeout <= 0 || sessionIdleFor(m, now) < timeout {
				protectedDirs[filepath.Clean(m.UserData)] = true
			}
		}
	}

	var killed []int
	for _, p := range procs {
		if protectedDirs[filepath.Clean(p.UserDataDir)] {
			continue // belongs to a session still within its idle window
		}
		killPID(p.PID)
		killed = append(killed, p.PID)
	}
	return killed
}

// maybeReap runs a sweep if one has not run recently. Called at the head of
// live-session ops. The stamp file rate-limits it so a chatty agent does not
// re-scan the directory on every click.
func maybeReap(c *connector.Ctx) {
	dir := sessionDir(c)
	stamp := filepath.Join(dir, reapStamp)
	if fi, err := os.Stat(stamp); err == nil {
		if sessionNow(c).Sub(fi.ModTime()) < reapInterval {
			return
		}
	}
	// Write the stamp BEFORE sweeping: if the sweep is slow, concurrent ops
	// should skip rather than pile up on the same scan.
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(stamp, []byte(sessionNow(c).Format(time.RFC3339)), 0o644)
	reapIdleSessions(c)
	// Orphan sweep runs after the metadata-driven reap so anything the latter
	// just terminated is already unprotected, and any process that outlived it
	// (a child that escaped, a re-forked PID) gets cleaned up in the same pass.
	if killOrphans(c) {
		reapOrphanBrowsers(c)
	}
}

// describeBlockingSessions renders the "why can't I open one" half of the cap
// error: which sessions hold the slots, how long each has been untouched, and
// how much longer before the reaper takes it. Written for an LLM caller, which
// otherwise has no way to tell "wait 30s" from "this will never free itself".
func describeBlockingSessions(c *connector.Ctx, live []sessionMeta) string {
	if len(live) == 0 {
		return "Close one with session_close before opening another (or set max_live_sessions to 0 for unlimited)."
	}
	now := sessionNow(c)
	parts := make([]string, 0, len(live))
	for _, m := range live {
		idle := sessionIdleFor(m, now)
		timeout := idleTimeoutFor(c, m)
		desc := m.ID + " (idle " + shortDur(idle)
		switch {
		case timeout <= 0:
			desc += ", no auto-timeout — must be closed explicitly)"
		case idle >= timeout:
			desc += ", past its timeout — will be reclaimed on the next sweep)"
		default:
			desc += ", auto-closes in " + shortDur(timeout-idle) + ")"
		}
		parts = append(parts, desc)
	}
	return "Holding the slot: " + strings.Join(parts, "; ") +
		". Either call session_close on one of them to free a slot now, or wait for the timeout."
}

// shortDur formats a duration the way a human reads it in a message ("4m",
// "2h11m"), instead of Go's default "4m0.0001s".
func shortDur(d time.Duration) string {
	if d < time.Minute {
		return strconv.Itoa(int(d.Seconds())) + "s"
	}
	d = d.Round(time.Minute)
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	if h > 0 {
		return strconv.Itoa(h) + "h" + strconv.Itoa(m) + "m"
	}
	return strconv.Itoa(m) + "m"
}

// ── profile lock hygiene ─────────────────────────────────────────────

// singletonFiles are the per-user-data-dir files Chrome uses to enforce "one
// browser per profile". They normally disappear on a clean exit; a killed
// browser leaves them behind pointing at a PID that no longer exists, and the
// next launch against that dir then fails.
var singletonFiles = []string{
	"SingletonLock",
	"SingletonSocket",
	"SingletonCookie",
	"DevToolsActivePort",
}

// clearStaleProfileLocks removes Chrome's singleton files from a user-data dir
// when they refer to a process that is gone. It is deliberately conservative:
// if the lock names a PID that is still alive, the files are left alone so a
// genuinely running browser is never sabotaged.
//
// Cost of removing them: Chrome treats the profile as freshly opened. For a
// named profile the cookies/logins in the dir itself still persist — only the
// crash-recovery state is lost.
func clearStaleProfileLocks(udd string) {
	if strings.TrimSpace(udd) == "" {
		return
	}
	if pid, ok := singletonOwnerPID(udd); ok && processAlive(pid) {
		return // a live browser owns this profile — leave it be
	}
	for _, name := range singletonFiles {
		_ = os.Remove(filepath.Join(udd, name))
	}
}

// singletonOwnerPID reads the PID out of SingletonLock. On Linux/macOS the lock
// is a symlink named "<host>-<pid>"; the target is what carries the pid. When
// the format is anything else (Windows, or a truncated file) it reports false
// and the caller falls back to treating the lock as stale.
func singletonOwnerPID(udd string) (int, bool) {
	target, err := os.Readlink(filepath.Join(udd, "SingletonLock"))
	if err != nil {
		return 0, false
	}
	i := strings.LastIndex(target, "-")
	if i < 0 || i == len(target)-1 {
		return 0, false
	}
	pid, err := strconv.Atoi(target[i+1:])
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}
