package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/pkg/connector"
)

// reapCtx builds a Ctx whose session dir is a temp dir, with optional config
// overrides. session_dir is a CONFIG value, so it goes in the cfg map.
func reapCtx(t *testing.T, cfg map[string]string) (*connector.Ctx, string) {
	t.Helper()
	dir := t.TempDir()
	if cfg == nil {
		cfg = map[string]string{}
	}
	cfg["session_dir"] = dir
	return connector.NewCtx(t.Context(), "test-instance", cfg, map[string]string{}, nil, nil, nil), dir
}

func TestSessionIdleFor_UsesLastUsed(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	m := sessionMeta{
		Created:  now.Add(-2 * time.Hour),
		LastUsed: now.Add(-5 * time.Minute),
	}
	if got := sessionIdleFor(m, now); got != 5*time.Minute {
		t.Fatalf("idle = %v, want 5m (LastUsed must win over Created)", got)
	}
}

// A session file written before LastUsed existed must fall back to Created,
// not be treated as idle since the zero time.
func TestSessionIdleFor_FallsBackToCreated(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	m := sessionMeta{Created: now.Add(-3 * time.Minute)}
	if got := sessionIdleFor(m, now); got != 3*time.Minute {
		t.Fatalf("idle = %v, want 3m", got)
	}
}

// No timestamps at all must not read as "idle forever" — that would reap a
// session the moment it appeared.
func TestSessionIdleFor_NoTimestampsIsNotIdle(t *testing.T) {
	if got := sessionIdleFor(sessionMeta{}, time.Now()); got != 0 {
		t.Fatalf("idle = %v, want 0 for a meta with no timestamps", got)
	}
}

func TestIdleTimeoutFor_NamedProfileGetsLongerLeash(t *testing.T) {
	c, _ := reapCtx(t, nil)
	anon := idleTimeoutFor(c, sessionMeta{})
	named := idleTimeoutFor(c, sessionMeta{Profile: "work"})
	if anon != defaultIdleTimeout {
		t.Fatalf("anonymous timeout = %v, want %v", anon, defaultIdleTimeout)
	}
	if named != anon*namedProfileFactor {
		t.Fatalf("named timeout = %v, want %v", named, anon*namedProfileFactor)
	}
}

func TestIdleTimeoutFor_NegativeDisables(t *testing.T) {
	c, _ := reapCtx(t, map[string]string{"browser_idle_timeout_min": "-1"})
	if got := idleTimeoutFor(c, sessionMeta{}); got != 0 {
		t.Fatalf("timeout = %v, want 0 (disabled) for a negative config", got)
	}
}

func TestIdleTimeoutFor_ConfigOverride(t *testing.T) {
	c, _ := reapCtx(t, map[string]string{"browser_idle_timeout_min": "30"})
	if got := idleTimeoutFor(c, sessionMeta{}); got != 30*time.Minute {
		t.Fatalf("timeout = %v, want 30m", got)
	}
}

// reapIdleSessions must remove an idle session's metadata. PID 0 is used so
// killPID is a no-op — this exercises the sweep decision, not process control.
func TestReapIdleSessions_RemovesIdleKeepsFresh(t *testing.T) {
	c, dir := reapCtx(t, map[string]string{"browser_idle_timeout_min": "5"})
	now := time.Now()

	stale := sessionMeta{ID: "stale-1", Created: now.Add(-time.Hour), LastUsed: now.Add(-10 * time.Minute)}
	fresh := sessionMeta{ID: "fresh-1", Created: now.Add(-time.Hour), LastUsed: now.Add(-time.Minute)}
	for _, m := range []sessionMeta{stale, fresh} {
		if err := writeMeta(dir, m); err != nil {
			t.Fatalf("writeMeta(%s): %v", m.ID, err)
		}
	}

	killed := reapIdleSessions(c)
	if len(killed) != 1 || killed[0] != "stale-1" {
		t.Fatalf("killed = %v, want [stale-1]", killed)
	}
	if _, err := os.Stat(metaPath(dir, "stale-1")); !os.IsNotExist(err) {
		t.Fatal("stale session metadata should have been removed")
	}
	if _, err := os.Stat(metaPath(dir, "fresh-1")); err != nil {
		t.Fatalf("fresh session must survive the sweep: %v", err)
	}
}

func TestReapIdleSessions_DisabledKeepsEverything(t *testing.T) {
	c, dir := reapCtx(t, map[string]string{"browser_idle_timeout_min": "-1"})
	m := sessionMeta{ID: "ancient", Created: time.Now().Add(-48 * time.Hour)}
	if err := writeMeta(dir, m); err != nil {
		t.Fatal(err)
	}
	if killed := reapIdleSessions(c); len(killed) != 0 {
		t.Fatalf("killed = %v, want none when reaping is disabled", killed)
	}
	if _, err := os.Stat(metaPath(dir, "ancient")); err != nil {
		t.Fatalf("session must survive with reaping disabled: %v", err)
	}
}

// touchSession is what keeps a session alive; without it the reaper would
// eventually take a browser that is actively in use.
func TestTouchSession_RefreshesLastUsed(t *testing.T) {
	c, dir := reapCtx(t, nil)
	old := time.Now().Add(-time.Hour)
	if err := writeMeta(dir, sessionMeta{ID: "s1", Created: old, LastUsed: old}); err != nil {
		t.Fatal(err)
	}
	touchSession(c, "s1")
	got, err := readMeta(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastUsed.After(old) {
		t.Fatalf("LastUsed = %v, want a time after %v", got.LastUsed, old)
	}
}

// maybeReap rate-limits itself; a second immediate call must not re-sweep.
// Verified by writing a now-idle session AFTER the first call: if the second
// call swept, the file would be gone.
func TestMaybeReap_RateLimited(t *testing.T) {
	c, dir := reapCtx(t, map[string]string{"browser_idle_timeout_min": "5"})
	maybeReap(c)

	idle := sessionMeta{ID: "idle-1", Created: time.Now().Add(-time.Hour), LastUsed: time.Now().Add(-time.Hour)}
	if err := writeMeta(dir, idle); err != nil {
		t.Fatal(err)
	}
	maybeReap(c) // within reapInterval → must be skipped
	if _, err := os.Stat(metaPath(dir, "idle-1")); err != nil {
		t.Fatalf("second maybeReap should have been rate-limited, but it swept: %v", err)
	}
}

// A lock naming a dead PID is stale and must be cleared, otherwise the next
// launch against that profile fails.
func TestClearStaleProfileLocks_RemovesDeadLock(t *testing.T) {
	udd := t.TempDir()
	for _, name := range singletonFiles {
		if err := os.WriteFile(filepath.Join(udd, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	clearStaleProfileLocks(udd)
	for _, name := range singletonFiles {
		if _, err := os.Stat(filepath.Join(udd, name)); !os.IsNotExist(err) {
			t.Fatalf("%s should have been removed", name)
		}
	}
}

func TestClearStaleProfileLocks_EmptyDirIsSafe(t *testing.T) {
	clearStaleProfileLocks("") // must not panic
	clearStaleProfileLocks(t.TempDir())
}

// Orphan sweeping must stay off unless explicitly enabled — it enumerates
// every process on the host.
func TestKillOrphans_DefaultOff(t *testing.T) {
	c, _ := reapCtx(t, nil)
	if killOrphans(c) {
		t.Fatal("orphan sweeping must default to off")
	}
	on, _ := reapCtx(t, map[string]string{"browser_kill_orphans": "true"})
	if !killOrphans(on) {
		t.Fatal("browser_kill_orphans=true must enable the sweep")
	}
}

// The sweep is scoped by --user-data-dir, so processes outside the session dir
// (a browser the user launched themselves) must never be enumerated.
func TestFindOwnedBrowserPIDs_IgnoresForeignProcesses(t *testing.T) {
	// This process does not carry a --user-data-dir flag, so a scan rooted at
	// a temp dir must not return it.
	for _, p := range findOwnedBrowserPIDs(t.TempDir()) {
		if p.PID == os.Getpid() {
			t.Fatal("the scan must never return the plugin's own process")
		}
	}
}

func TestFindOwnedBrowserPIDs_EmptyRootIsNoop(t *testing.T) {
	if got := findOwnedBrowserPIDs(""); got != nil {
		t.Fatalf("empty root must return nil, got %v", got)
	}
}

// A live, recently-used session must survive the orphan sweep even though its
// PID is fake — protection is keyed on the user-data dir, which is what keeps
// an engine's self-forked children safe too.
func TestReapOrphanBrowsers_ProtectsActiveSessionDirs(t *testing.T) {
	c, dir := reapCtx(t, map[string]string{"browser_idle_timeout_min": "10"})
	udd := filepath.Join(dir, "profile-active")
	if err := os.MkdirAll(udd, 0o755); err != nil {
		t.Fatal(err)
	}
	m := sessionMeta{ID: "live-1", PID: 999999, UserData: udd, Created: time.Now(), LastUsed: time.Now()}
	if err := writeMeta(dir, m); err != nil {
		t.Fatal(err)
	}
	// No real browser runs under this dir, so nothing should be killed; the
	// assertion that matters is that it does not panic and reports no kills.
	if killed := reapOrphanBrowsers(c); len(killed) != 0 {
		t.Fatalf("killed = %v, want none (no real processes under the test dir)", killed)
	}
}

func TestShortDur(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{4 * time.Minute, "4m"},
		{90 * time.Minute, "1h30m"},
		{2 * time.Hour, "2h0m"},
	}
	for _, tc := range cases {
		if got := shortDur(tc.in); got != tc.want {
			t.Errorf("shortDur(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// The cap error must tell the caller which session blocks it and when that
// session frees itself — an LLM cannot otherwise tell "retry soon" from
// "this never resolves on its own".
func TestDescribeBlockingSessions_ReportsCountdown(t *testing.T) {
	c, _ := reapCtx(t, map[string]string{"browser_idle_timeout_min": "10"})
	live := []sessionMeta{{ID: "s1", Created: time.Now(), LastUsed: time.Now().Add(-4 * time.Minute)}}
	got := describeBlockingSessions(c, live)
	if !strings.Contains(got, "s1") {
		t.Errorf("message must name the blocking session: %q", got)
	}
	if !strings.Contains(got, "auto-closes in") {
		t.Errorf("message must state the countdown: %q", got)
	}
	if !strings.Contains(got, "session_close") {
		t.Errorf("message must offer the manual escape hatch: %q", got)
	}
}

func TestDescribeBlockingSessions_NoTimeoutSaysSo(t *testing.T) {
	c, _ := reapCtx(t, map[string]string{"browser_idle_timeout_min": "-1"})
	live := []sessionMeta{{ID: "s1", Created: time.Now()}}
	got := describeBlockingSessions(c, live)
	if !strings.Contains(got, "no auto-timeout") {
		t.Errorf("with reaping disabled the message must say so: %q", got)
	}
}
