package agents

import (
	"errors"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/logintty"
)

// recoveryHarness is the checker with every outside effect faked: a
// clock the test moves, one claude-like instance on login "k", a probe
// that counts calls and writes whatever reading the test queued.
type recoveryHarness struct {
	r       *failoverRecovery
	now     time.Time
	probes  int
	reading []provider.UsageDiskWindow // nil = probe gets nothing new
	usage   bool
	conn    bool
	changed time.Time
	queued  []func()
}

func newRecoveryHarness(t *testing.T, recheck int) *recoveryHarness {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(provider.SetHealthDirForTest(dir))
	t.Cleanup(provider.SetUsageDiskDirForTest(dir + "/usage"))
	h := &recoveryHarness{now: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC), usage: true}
	ins := provider.Instance{Type: provider.TypeClaude, Name: "a", Failover: &provider.FailoverConfig{RecheckMinutes: recheck}}
	h.r = &failoverRecovery{
		now:           func() time.Time { return h.now },
		accounts:      provider.UnhealthyAccounts,
		instances:     func() ([]provider.Instance, error) { return []provider.Instance{ins}, nil },
		keyOf:         func(provider.Instance) string { return "k" },
		supportsUsage: func(provider.Type) bool { return h.usage },
		probe: func(provider.Instance) {
			h.probes++
			if h.reading != nil {
				provider.RecordUsage(provider.UsageDisk{Key: "k", ObservedAt: h.now, Windows: h.reading})
			}
		},
		login:    func(provider.Instance) (bool, time.Time) { return h.conn, h.changed },
		run:      func(f func()) { f() },
		inflight: map[string]bool{},
	}
	return h
}

func (h *recoveryHarness) healthy() bool {
	_, bad := provider.HealthOf("k")
	return !bad
}

func TestFailoverRecoveryWaitsForUntilAndRecheck(t *testing.T) {
	h := newRecoveryHarness(t, 10)
	start := h.now
	provider.MarkUnhealthy("k", provider.Health{Kind: event.ErrLimit, Since: start, CheckedAt: start, Until: start.Add(30 * time.Minute)})
	h.reading = []provider.UsageDiskWindow{{Key: "five_hour", Utilization: 100, ResetsAt: start.Add(5 * time.Hour)}}

	h.now = start.Add(20 * time.Minute) // recheck interval passed, Until not
	h.r.tick()
	if h.probes != 0 {
		t.Fatalf("probed %d times before Until", h.probes)
	}

	h.now = start.Add(31 * time.Minute) // due
	h.r.tick()
	if h.probes != 1 || h.healthy() {
		t.Fatalf("due: probes=%d healthy=%v, want 1/false (still full)", h.probes, h.healthy())
	}

	h.now = start.Add(36 * time.Minute) // within the recheck interval of the last check
	h.r.tick()
	if h.probes != 1 {
		t.Fatalf("re-probed inside the recheck interval: %d", h.probes)
	}

	h.now = start.Add(42 * time.Minute)
	h.reading = []provider.UsageDiskWindow{{Key: "five_hour", Utilization: 8}}
	h.r.tick()
	if h.probes != 2 || !h.healthy() {
		t.Fatalf("second due: probes=%d healthy=%v, want 2/true", h.probes, h.healthy())
	}
}

func TestFailoverRecoveryManualResetBeforeUntil(t *testing.T) {
	h := newRecoveryHarness(t, 10)
	start := h.now
	provider.MarkUnhealthy("k", provider.Health{Kind: event.ErrLimit, Since: start, CheckedAt: start, Until: start.Add(4 * time.Hour)})
	// Someone reset the limit by hand; the Re-check wrote this reading.
	provider.UsageDiskStore(provider.UsageDisk{Key: "k", ObservedAt: start.Add(2 * time.Minute), Windows: []provider.UsageDiskWindow{{Key: "w", Utilization: 0}}})
	h.now = start.Add(3 * time.Minute)
	h.r.tick()
	if h.probes != 0 || !h.healthy() {
		t.Fatalf("probes=%d healthy=%v, want 0/true", h.probes, h.healthy())
	}
}

func TestFailoverRecoveryNoUsageTypeIsTimeBased(t *testing.T) {
	h := newRecoveryHarness(t, 5)
	h.usage = false
	start := h.now
	provider.MarkUnhealthy("k", provider.Health{Kind: event.ErrLimit, Since: start, CheckedAt: start, Until: start.Add(time.Hour)})
	h.now = start.Add(30 * time.Minute)
	h.r.tick()
	if h.healthy() {
		t.Fatal("recovered before Until")
	}
	h.now = start.Add(61 * time.Minute)
	h.r.tick()
	if h.probes != 0 || !h.healthy() {
		t.Fatalf("probes=%d healthy=%v, want 0/true", h.probes, h.healthy())
	}
}

func TestFailoverRecoveryAuth(t *testing.T) {
	h := newRecoveryHarness(t, 10)
	start := h.now
	provider.MarkUnhealthy("k", provider.Health{Kind: event.ErrAuth, Since: start, CheckedAt: start})
	h.conn, h.changed = true, start.Add(-time.Hour) // login not rewritten since
	h.now = start.Add(11 * time.Minute)
	h.r.tick()
	if h.healthy() {
		t.Fatal("an unchanged login must not count as recovered")
	}
	if hh, _ := provider.HealthOf("k"); hh.Tries != 1 || !hh.RetryAt.Equal(start.Add(10*time.Minute)) {
		t.Fatalf("retry window not scheduled: %+v", hh)
	}
	h.changed = start.Add(15 * time.Minute) // /login ran
	h.now = start.Add(22 * time.Minute)
	h.r.tick()
	if h.probes != 0 || !h.healthy() {
		t.Fatalf("probes=%d healthy=%v, want 0/true", h.probes, h.healthy())
	}
}

func TestFailoverRecoverySingleFlight(t *testing.T) {
	h := newRecoveryHarness(t, 10)
	start := h.now
	provider.MarkUnhealthy("k", provider.Health{Kind: event.ErrLimit, Since: start, CheckedAt: start})
	h.reading = []provider.UsageDiskWindow{{Key: "w", Utilization: 100}} // stays limited
	h.r.run = func(f func()) { h.queued = append(h.queued, f) }          // hold the check
	h.now = start.Add(11 * time.Minute)
	h.r.tick()
	h.r.tick()
	if len(h.queued) != 1 {
		t.Fatalf("checks started = %d, want 1", len(h.queued))
	}
	h.queued[0]()
	h.r.tick()
	if len(h.queued) != 2 {
		t.Fatalf("after the first finished, checks = %d, want 2", len(h.queued))
	}
}

func TestFailoverRecoveryForgetsRemovedLogin(t *testing.T) {
	h := newRecoveryHarness(t, 10)
	provider.MarkUnhealthy("gone", provider.Health{Kind: event.ErrLimit, Since: h.now})
	h.r.tick()
	if _, ok := provider.HealthOf("gone"); ok {
		t.Fatal("an account no instance uses should be dropped")
	}
}

func TestRecheckEvery(t *testing.T) {
	ins := func(m int) provider.Instance {
		return provider.Instance{Failover: &provider.FailoverConfig{RecheckMinutes: m}}
	}
	if got := recheckEvery([]provider.Instance{{}}); got != 10*time.Minute {
		t.Fatalf("default = %v", got)
	}
	if got := recheckEvery([]provider.Instance{ins(30), ins(5)}); got != 5*time.Minute {
		t.Fatalf("shortest = %v", got)
	}
	if got := recheckEvery([]provider.Instance{ins(500)}); got != 60*time.Minute {
		t.Fatalf("clamped = %v", got)
	}
}

// H1: a limit with no reset time and a usage API that never answers
// still comes back, on time, once a recheck interval has passed.
func TestFailoverRecoveryBrokenUsageFallsBackToTime(t *testing.T) {
	h := newRecoveryHarness(t, 10)
	start := h.now
	provider.MarkUnhealthy("k", provider.Health{Kind: event.ErrLimit, Since: start, CheckedAt: start})
	h.reading = nil // every probe fails / is declined
	h.now = start.Add(5 * time.Minute)
	h.r.tick()
	if h.probes != 0 || h.healthy() {
		t.Fatalf("before Since+recheck: probes=%d healthy=%v", h.probes, h.healthy())
	}
	h.now = start.Add(10 * time.Minute)
	h.r.tick()
	if h.probes != 1 || !h.healthy() {
		t.Fatalf("after Since+recheck: probes=%d healthy=%v, want 1/true", h.probes, h.healthy())
	}
}

// L2: a failure recorded while a check was deciding survives it.
func TestFailoverRecoveryKeepsNewerFailure(t *testing.T) {
	h := newRecoveryHarness(t, 10)
	start := h.now
	provider.MarkUnhealthy("k", provider.Health{Kind: event.ErrLimit, Since: start, CheckedAt: start, Until: start.Add(time.Minute)})
	h.now = start.Add(11 * time.Minute)
	h.r.probe = func(provider.Instance) {
		h.probes++
		provider.MarkHealthy("k")
		provider.MarkUnhealthy("k", provider.Health{Kind: event.ErrLimit, Since: h.now, CheckedAt: h.now, Until: h.now.Add(time.Hour)})
	}
	h.r.tick()
	if h.healthy() {
		t.Fatal("a failure recorded during the check was erased")
	}
}

func usageDiskTestDirs(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(provider.SetHealthDirForTest(dir))
	t.Cleanup(provider.SetUsageDiskDirForTest(dir + "/usage"))
}

// M6: a reading loaded from disk is served at once; a stale one gets ONE
// background refresh in this process, a fresh one none.
func TestUsageCacheDiskStartRefreshesStaleOnce(t *testing.T) {
	usageDiskTestDirs(t)
	clk := &fixedClock{t: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)}
	provider.UsageDiskStore(provider.UsageDisk{Key: "stale", ObservedAt: clk.now().Add(-time.Hour), Windows: []provider.UsageDiskWindow{{Key: "five_hour", Utilization: 55}}})
	provider.UsageDiskStore(provider.UsageDisk{Key: "fresh", ObservedAt: clk.now().Add(-30 * time.Second), Windows: []provider.UsageDiskWindow{{Key: "five_hour", Utilization: 40}}})

	c := testUsageCache(time.Minute)
	c.now = clk.now
	hold := []func(){}
	c.run = func(f func()) { hold = append(hold, f) } // keep the probe pending
	calls := 0
	fetch := func() ([]logintty.UsageWindow, error) {
		calls++
		return []logintty.UsageWindow{{Key: "five_hour", Utilization: 70}}, nil
	}
	v := c.get("stale", fetch, time.Time{})
	if !v.Known || v.Windows[0].Utilization != 55 || len(hold) != 1 {
		t.Fatalf("stale: served %+v, probes started %d; want file value + 1 background probe", v, len(hold))
	}
	hold[0]()
	c.get("stale", fetch, time.Time{})
	if len(hold) != 1 || calls != 1 {
		t.Fatalf("stale refreshed more than once: started %d, calls %d", len(hold), calls)
	}
	if d, _ := provider.UsageDiskLookup("stale"); d.Windows[0].Utilization != 70 {
		t.Fatalf("refresh not written back: %+v", d)
	}
	c.get("fresh", fetch, time.Time{})
	if len(hold) != 1 {
		t.Fatal("a fresh file reading was probed")
	}
}

// M5: failed probes back off exponentially, and the bookkeeping (plus a
// server Retry-After) survives a restart through the file.
func TestUsageCacheAutoBackoffPersists(t *testing.T) {
	usageDiskTestDirs(t)
	clk := &fixedClock{t: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)}
	c := testUsageCache(time.Minute)
	c.now = clk.now
	fail := func() ([]logintty.UsageWindow, error) { return nil, errors.New("503") }

	if c.autoRefresh("k", fail) == nil {
		t.Fatal("first auto refresh declined")
	}
	clk.advance(time.Minute)
	if c.autoRefresh("k", fail) == nil {
		t.Fatal("after 1 failure the 1m floor should allow a retry")
	}
	clk.advance(time.Minute) // 2 failures → 2m backoff
	if c.autoRefresh("k", fail) != nil {
		t.Fatal("after 2 failures a retry at +1m must be declined")
	}
	clk.advance(time.Minute)
	if c.autoRefresh("k", fail) == nil {
		t.Fatal("after 2 failures a retry at +2m should run")
	}
	if got := autoBackoff(20); got != usageAutoMaxBackoff {
		t.Fatalf("backoff cap = %v", got)
	}

	// Server cooldown, then a restart: the new cache reads it back.
	clk.advance(time.Hour)
	limited := func() ([]logintty.UsageWindow, error) {
		return nil, &logintty.RateLimitedError{Status: "429", RetryAfter: time.Hour}
	}
	c.autoRefresh("k", limited)
	d, ok := provider.UsageDiskLookup("k")
	if !ok || d.Fails != 4 || !d.ServerUntil.Equal(clk.now().Add(time.Hour)) {
		t.Fatalf("bookkeeping on disk = %+v, %v", d, ok)
	}
	c2 := testUsageCache(time.Minute)
	c2.now = clk.now
	clk.advance(30 * time.Minute)
	if c2.autoRefresh("k", fail) != nil {
		t.Fatal("restart probed inside the server's Retry-After")
	}
	if _, wait := c2.forceRefresh("k", fail); wait <= 0 {
		t.Fatal("Re-check after restart ignored the persisted Retry-After")
	}
}

// An unsupported verdict drops the file; a plain failure keeps the last
// good reading on disk.
func TestUsageCachePersistOnFailure(t *testing.T) {
	usageDiskTestDirs(t)
	clk := &fixedClock{t: time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)}
	c := testUsageCache(time.Minute)
	c.now = clk.now
	c.autoRefresh("k", func() ([]logintty.UsageWindow, error) {
		return []logintty.UsageWindow{{Key: "w", Utilization: 30}}, nil
	})
	clk.advance(time.Hour)
	c.autoRefresh("k", func() ([]logintty.UsageWindow, error) { return nil, errors.New("timeout") })
	d, ok := provider.UsageDiskLookup("k")
	if !ok || len(d.Windows) != 1 || d.Windows[0].Utilization != 30 || d.Fails != 1 {
		t.Fatalf("failure replaced the good reading: %+v, %v", d, ok)
	}
	clk.advance(time.Hour)
	c.autoRefresh("k", func() ([]logintty.UsageWindow, error) { return nil, logintty.ErrUsageUnsupported })
	if _, ok := provider.UsageDiskLookup("k"); ok {
		t.Fatal("unsupported verdict must drop the file")
	}
}

// R1: an auth failure with nothing proving it fixed gets backed-off
// tentative retry windows; a changed instance fingerprint recovers it;
// ResetHealth clears it outright.
func TestFailoverRecoveryAuthRetryWindows(t *testing.T) {
	h := newRecoveryHarness(t, 10)
	start := h.now
	fp := provider.InstanceFingerprint(provider.Instance{Type: provider.TypeClaude, Name: "a", Failover: &provider.FailoverConfig{RecheckMinutes: 10}})
	provider.MarkUnhealthy("k", provider.Health{Kind: event.ErrAuth, Since: start, CheckedAt: start, Fingerprint: fp})
	h.conn = true // credentials present, never rewritten
	h.now = start.Add(10 * time.Minute)
	h.r.tick()
	if ok, _ := provider.HealthyNow("k", h.now); !ok {
		t.Fatal("first retry window (10m) not open")
	}
	// The tentative turn fails again at 12m: window closes, next is 20m later.
	fail2 := start.Add(12 * time.Minute)
	provider.MarkUnhealthy("k", provider.Health{Kind: event.ErrAuth, Since: fail2, CheckedAt: fail2, Fingerprint: fp})
	if ok, _ := provider.HealthyNow("k", fail2.Add(time.Minute)); ok {
		t.Fatal("window stayed open after a new failure")
	}
	h.now = start.Add(23 * time.Minute)
	h.r.tick()
	hh, _ := provider.HealthOf("k")
	if hh.Tries != 2 || !hh.RetryAt.Equal(fail2.Add(20*time.Minute)) {
		t.Fatalf("second window = %+v, want tries 2 at +20m", hh)
	}
	if ok, _ := provider.HealthyNow("k", fail2.Add(19*time.Minute)); ok {
		t.Fatal("second window opened early")
	}
	if got := provider.AuthRetryBackoff(3); got != 40*time.Minute {
		t.Fatalf("third backoff = %v", got)
	}
	if got := provider.AuthRetryBackoff(9); got != time.Hour {
		t.Fatalf("backoff cap = %v", got)
	}
	provider.ResetHealth("k")
	if !h.healthy() {
		t.Fatal("ResetHealth did not clear")
	}
}

func TestFailoverRecoveryAuthFingerprintChange(t *testing.T) {
	h := newRecoveryHarness(t, 10)
	start := h.now
	provider.MarkUnhealthy("k", provider.Health{Kind: event.ErrAuth, Since: start, CheckedAt: start, Fingerprint: "old-config"})
	h.now = start.Add(11 * time.Minute)
	h.r.tick()
	if !h.healthy() {
		t.Fatal("a changed instance fingerprint must count as recovered")
	}
}

// R4: a failure-only file shows its error after a restart.
func TestUsageCacheLoadsLastError(t *testing.T) {
	usageDiskTestDirs(t)
	at := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	provider.UsageDiskStore(provider.UsageDisk{Key: "k", AttemptedAt: at, Fails: 1, LastError: "401 unauthorized"})
	c := testUsageCache(time.Minute)
	c.now = func() time.Time { return at.Add(time.Second) }
	v := c.get("k", func() ([]logintty.UsageWindow, error) { return nil, errors.New("x") }, time.Time{})
	if v.Err == nil || v.Err.Error() != "401 unauthorized" {
		t.Fatalf("view = %+v", v)
	}
}
