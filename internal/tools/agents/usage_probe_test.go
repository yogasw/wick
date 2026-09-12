package agents

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider/logintty"
)

// testUsageCache is the production cache wired for tests: refreshes run
// inline on the calling goroutine and the pacing gate's wait is recorded
// instead of slept, so the ordering rules can be asserted without time
// passing.
func testUsageCache(ttl time.Duration) *usageCache {
	c := newUsageCache(ttl)
	c.run = func(f func()) { f() }
	c.sleep = func(time.Duration) {}
	return c
}

// fixedClock drives the cache's notion of now.
type fixedClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fixedClock { return &fixedClock{t: time.Now()} }

func (c *fixedClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fixedClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func clockedCache(ttl time.Duration) (*usageCache, *fixedClock) {
	c := testUsageCache(ttl)
	clk := newClock()
	c.now = clk.now
	c.gate.now = clk.now
	return c, clk
}

func windows(u float64) []logintty.UsageWindow {
	return []logintty.UsageWindow{{Key: "five_hour", Utilization: u}}
}

func TestUsageCacheServesWithinTTL(t *testing.T) {
	c, clk := clockedCache(time.Minute)
	calls := 0
	fetch := func() ([]logintty.UsageWindow, error) {
		calls++
		return windows(7), nil
	}

	first := c.get("acct", fetch).Windows
	clk.advance(30 * time.Second)
	v := c.get("acct", fetch)
	second, known := v.Windows, v.Known

	if calls != 1 {
		t.Errorf("fetched %d times, want 1 — the second read is inside the TTL", calls)
	}
	if !known || len(first) != 1 || len(second) != 1 || second[0].Utilization != 7 {
		t.Errorf("cached windows not returned: first=%+v second=%+v known=%v", first, second, known)
	}
}

func TestUsageCacheRefetchesAfterTTL(t *testing.T) {
	c, clk := clockedCache(time.Minute)
	calls := 0
	fetch := func() ([]logintty.UsageWindow, error) {
		calls++
		return windows(float64(calls)), nil
	}

	c.get("acct", fetch)
	clk.advance(2 * time.Minute)
	got := c.get("acct", fetch).Windows

	if calls != 2 {
		t.Errorf("fetched %d times, want 2 — the TTL expired", calls)
	}
	if len(got) != 1 || got[0].Utilization != 2 {
		t.Errorf("windows = %+v, want the refetched value", got)
	}
}

func TestUsageCacheKeysPerAccount(t *testing.T) {
	c, _ := clockedCache(time.Minute)
	var asked []string
	mk := func(key string) usageFetch {
		return func() ([]logintty.UsageWindow, error) {
			asked = append(asked, key)
			return nil, nil
		}
	}

	c.get("a@abc.com", mk("a@abc.com"))
	c.get("b@abc.com", mk("b@abc.com"))

	if len(asked) != 2 {
		t.Errorf("fetched for %v, want both accounts probed — they are separate logins", asked)
	}
}

// The bug this whole file exists for: a failed probe used not to be
// cached, so a 429 was retried on every 4-second page poll, which kept
// the rate limit alive. A failure must park the account instead.
func TestUsageCacheCachesFailureWithBackoff(t *testing.T) {
	c, clk := clockedCache(time.Minute)
	calls := 0
	fetch := func() ([]logintty.UsageWindow, error) {
		calls++
		return nil, errors.New("boom")
	}

	v1 := c.get("acct", fetch)
	err1, known := v1.Err, v1.Known
	clk.advance(4 * time.Second) // the providers page's poll interval
	err2 := c.get("acct", fetch).Err

	if err1 == nil || err2 == nil {
		t.Fatalf("errors swallowed: %v / %v", err1, err2)
	}
	if !known {
		t.Error("known = false after a failure — the failure is a known state to display")
	}
	if calls != 1 {
		t.Errorf("fetched %d times, want 1 — a failure must be cached, not retried on the next poll", calls)
	}

	clk.advance(usageErrBackoffMax) // well past any backoff
	c.get("acct", fetch)
	if calls != 2 {
		t.Errorf("fetched %d times after the backoff expired, want 2 — it must eventually retry", calls)
	}
}

func TestUsageCacheRateLimitBacksOffLongerThanOrdinaryFailure(t *testing.T) {
	rl := &logintty.RateLimitedError{Status: "429 Too Many Requests"}
	plain := errors.New("dial tcp: timeout")

	if got := usageBackoff(rl, 1); got < usageRateLimitBackoffMin {
		t.Errorf("429 backoff = %v, want at least %v", got, usageRateLimitBackoffMin)
	}
	if got := usageBackoff(plain, 1); got >= usageRateLimitBackoffMin {
		t.Errorf("ordinary backoff = %v, want shorter than the rate-limit floor", got)
	}
	// Repeated failures must grow, and stop growing at the ceiling.
	if usageBackoff(plain, 3) <= usageBackoff(plain, 1) {
		t.Error("backoff does not grow with consecutive failures")
	}
	if got := usageBackoff(plain, 30); got > usageErrBackoffMax+usageErrBackoffMax/4 {
		t.Errorf("backoff = %v, want capped near %v", got, usageErrBackoffMax)
	}
}

// When the endpoint says how long to wait, that wins over our own guess.
func TestUsageCacheHonoursRetryAfter(t *testing.T) {
	c, clk := clockedCache(time.Minute)
	calls := 0
	fetch := func() ([]logintty.UsageWindow, error) {
		calls++
		return nil, &logintty.RateLimitedError{Status: "429 Too Many Requests", RetryAfter: 90 * time.Second}
	}

	c.get("acct", fetch)
	clk.advance(80 * time.Second)
	c.get("acct", fetch)
	if calls != 1 {
		t.Errorf("fetched %d times, want 1 — still inside the server's Retry-After", calls)
	}

	clk.advance(30 * time.Second) // past 90s + the small jitter
	c.get("acct", fetch)
	if calls != 2 {
		t.Errorf("fetched %d times, want 2 — the Retry-After window has passed", calls)
	}
}

// A refused refresh must not blank a card that already has numbers.
func TestUsageCacheServesLastGoodWhenRefreshFails(t *testing.T) {
	c, clk := clockedCache(time.Minute)
	fail := false
	fetch := func() ([]logintty.UsageWindow, error) {
		if fail {
			return nil, &logintty.RateLimitedError{Status: "429 Too Many Requests"}
		}
		return windows(76), nil
	}

	c.get("acct", fetch)
	fail = true
	clk.advance(2 * time.Minute) // TTL expired, refresh will fail
	v2 := c.get("acct", fetch)
	got, err, known := v2.Windows, v2.Err, v2.Known

	if err != nil {
		t.Errorf("err = %v, want the stale-but-good reading instead", err)
	}
	if !known || len(got) != 1 || got[0].Utilization != 76 {
		t.Errorf("windows = %+v (known=%v), want the last good reading", got, known)
	}

	// Once it is older than the stale window, stop pretending.
	clk.advance(usageStaleWindow + time.Minute)
	v3 := c.get("acct", fetch)
	err, known = v3.Err, v3.Known
	if err == nil || !known {
		t.Errorf("err = %v, want the failure surfaced once the reading is too old", err)
	}
}

func TestUsageCacheCachesUnsupportedVerdict(t *testing.T) {
	c, clk := clockedCache(time.Minute)
	calls := 0
	fetch := func() ([]logintty.UsageWindow, error) {
		calls++
		return nil, logintty.ErrUsageUnsupported
	}

	err1 := c.get("acct", fetch).Err
	clk.advance(time.Hour)
	err2 := c.get("acct", fetch).Err

	// "This provider type has no usage API" is a property of the build,
	// not a transient failure — re-asking every page load is pointless.
	if calls != 1 {
		t.Errorf("fetched %d times, want 1 — unsupported is a stable verdict", calls)
	}
	if !errors.Is(err1, logintty.ErrUsageUnsupported) || !errors.Is(err2, logintty.ErrUsageUnsupported) {
		t.Errorf("verdict not preserved: %v / %v", err1, err2)
	}
}

// Two readers arriving in the same instant for the same account: the
// second must wait on the first probe, not start its own.
func TestUsageCacheSingleFlightsConcurrentReaders(t *testing.T) {
	c := newUsageCache(time.Minute) // real goroutines, real gate
	c.sleep = func(time.Duration) {}
	var calls int32
	release := make(chan struct{})
	var mu sync.Mutex
	fetch := func() ([]logintty.UsageWindow, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		<-release // hold the flight open while the other readers arrive
		return windows(42), nil
	}

	const readers = 8
	var wg sync.WaitGroup
	got := make([]float64, readers)
	for i := range readers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			w := c.getWait(context.Background(), "acct", fetch).Windows
			if len(w) == 1 {
				got[i] = w[0].Utilization
			}
		}(i)
	}
	// Give every reader time to queue behind the in-flight probe.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	mu.Lock()
	n := calls
	mu.Unlock()
	if n != 1 {
		t.Errorf("fetched %d times for %d simultaneous readers, want exactly 1", n, readers)
	}
	for i, u := range got {
		if u != 42 {
			t.Errorf("reader %d got %v, want the shared probe's result 42", i, u)
		}
	}
}

// getWait must not hang past its caller's budget: the page still paints,
// and the probe keeps running for whoever reads next.
func TestUsageCacheGetWaitRespectsContext(t *testing.T) {
	c := newUsageCache(time.Minute)
	c.sleep = func(time.Duration) {}
	block := make(chan struct{})
	defer close(block)
	fetch := func() ([]logintty.UsageWindow, error) {
		<-block
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		known := c.getWait(ctx, "acct", fetch).Known
		if known {
			t.Error("known = true, want a blank when the wait timed out")
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("getWait ignored its context deadline")
	}
}

// The pacing rule: two probes reserved at the same instant come out
// separated by a random gap inside the configured band.
func TestPaceGateSpacesProbesRandomly(t *testing.T) {
	clk := newClock()
	g := newPaceGate(usagePaceMin, usagePaceMax)
	g.now = clk.now

	if first := g.reserve(); first != 0 {
		t.Errorf("first probe waited %v, want it to go immediately", first)
	}
	second := g.reserve()
	if second < usagePaceMin || second > usagePaceMax {
		t.Errorf("second probe waits %v, want a gap inside [%v, %v]", second, usagePaceMin, usagePaceMax)
	}
	third := g.reserve()
	if third < second+usagePaceMin || third > second+usagePaceMax {
		t.Errorf("third probe waits %v, want %v plus another gap", third, second)
	}

	// Randomised, not a fixed interval: a constant gap would let
	// independent processes settle into lockstep.
	seen := map[time.Duration]bool{}
	for range 40 {
		seen[randomDuration(usagePaceMin, usagePaceMax)] = true
	}
	if len(seen) < 5 {
		t.Errorf("randomDuration produced %d distinct values in 40 draws, want a spread", len(seen))
	}
}

// Time only moves in the gate when the caller actually waits, so a gap
// that has already elapsed costs nothing.
func TestPaceGateDoesNotDelayWhenTheGapAlreadyPassed(t *testing.T) {
	clk := newClock()
	g := newPaceGate(usagePaceMin, usagePaceMax)
	g.now = clk.now

	g.reserve()
	clk.advance(usagePaceMax + time.Second)
	if got := g.reserve(); got != 0 {
		t.Errorf("waited %v, want 0 — the previous slot is long gone", got)
	}
}

// The Retry button on a card that already has numbers: a re-check must
// be allowed, not refused just because the reading is still fresh.
func TestForceRefreshOverridesTheTTL(t *testing.T) {
	c, clk := clockedCache(time.Minute)
	calls := 0
	fetch := func() ([]logintty.UsageWindow, error) {
		calls++
		return windows(float64(calls)), nil
	}

	c.get("acct", fetch)
	clk.advance(usageManualMinInterval) // clear the manual floor only
	accepted, wait := c.forceRefresh("acct", fetch)

	if !accepted || wait != 0 {
		t.Fatalf("accepted=%v wait=%v, want an accepted re-check", accepted, wait)
	}
	if calls != 2 {
		t.Errorf("fetched %d times, want 2 — the button must beat the TTL", calls)
	}
	if got := c.get("acct", fetch).Windows; len(got) != 1 || got[0].Utilization != 2 {
		t.Errorf("windows = %+v, want the re-checked value", got)
	}
}

// …and on a card that is in backoff after a failure, which is the case
// the button exists for.
func TestForceRefreshOverridesOurOwnBackoff(t *testing.T) {
	c, clk := clockedCache(time.Minute)
	calls := 0
	fail := true
	fetch := func() ([]logintty.UsageWindow, error) {
		calls++
		if fail {
			return nil, errors.New("boom")
		}
		return windows(50), nil
	}

	c.get("acct", fetch) // fails, parks the account for usageErrBackoffMin
	fail = false
	clk.advance(usageManualMinInterval)
	if accepted, _ := c.forceRefresh("acct", fetch); !accepted {
		t.Fatal("refresh refused while only our own backoff was in the way")
	}
	if calls != 2 {
		t.Errorf("fetched %d times, want 2", calls)
	}
	if v := c.get("acct", fetch); v.Err != nil || len(v.Windows) != 1 {
		t.Errorf("view = %+v, want the recovered reading", v)
	}
}

// What the button must NOT do: hand the user a way to re-create the
// rate limit one click at a time.
func TestForceRefreshRefusesInsideTheServerCooldown(t *testing.T) {
	c, clk := clockedCache(time.Minute)
	calls := 0
	fetch := func() ([]logintty.UsageWindow, error) {
		calls++
		return nil, &logintty.RateLimitedError{Status: "429 Too Many Requests", RetryAfter: 5 * time.Minute}
	}

	c.get("acct", fetch)
	clk.advance(time.Minute)
	accepted, wait := c.forceRefresh("acct", fetch)

	if accepted {
		t.Error("accepted a manual probe inside the server's Retry-After — that earns the next 429")
	}
	if wait < 3*time.Minute || wait > 4*time.Minute {
		t.Errorf("wait = %v, want the ~4m remaining of the server's cooldown", wait)
	}
	if calls != 1 {
		t.Errorf("fetched %d times, want 1", calls)
	}

	clk.advance(5 * time.Minute) // cooldown over
	if accepted, _ := c.forceRefresh("acct", fetch); !accepted {
		t.Error("still refusing after the server's cooldown expired")
	}
}

func TestForceRefreshRefusesRapidClicks(t *testing.T) {
	c, clk := clockedCache(time.Minute)
	calls := 0
	fetch := func() ([]logintty.UsageWindow, error) {
		calls++
		return windows(1), nil
	}

	c.get("acct", fetch)
	clk.advance(2 * time.Second)
	accepted, wait := c.forceRefresh("acct", fetch)

	if accepted {
		t.Error("second probe accepted 2s after the first — the floor is not holding")
	}
	if wait <= 0 || wait > usageManualMinInterval {
		t.Errorf("wait = %v, want the remainder of the manual floor", wait)
	}
	if calls != 1 {
		t.Errorf("fetched %d times, want 1", calls)
	}
}

// A click while a probe is already running is not a refusal — the user
// asked for a check and a check is happening.
func TestForceRefreshAcceptsWhileAProbeIsRunning(t *testing.T) {
	c := newUsageCache(time.Minute)
	c.sleep = func(time.Duration) {}
	release := make(chan struct{})
	var mu sync.Mutex
	calls := 0
	fetch := func() ([]logintty.UsageWindow, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		<-release
		return windows(9), nil
	}

	go func() { c.get("acct", fetch) }()
	time.Sleep(50 * time.Millisecond)

	accepted, wait := c.forceRefresh("acct", fetch)
	if !accepted || wait != 0 {
		t.Errorf("accepted=%v wait=%v, want the in-flight probe to count as accepted", accepted, wait)
	}
	close(release)
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("fetched %d times, want 1 — the click must not add a second flight", calls)
	}
}

// The "checking" light: true while a probe is in flight, false once it
// has landed. Without it the page cannot tell "working on it" from
// "this number is just old".
func TestViewReportsCheckingWhileProbeRuns(t *testing.T) {
	c := newUsageCache(time.Minute)
	c.sleep = func(time.Duration) {}
	release := make(chan struct{})
	fetch := func() ([]logintty.UsageWindow, error) {
		<-release
		return windows(12), nil
	}

	go func() { c.get("acct", fetch) }()
	time.Sleep(50 * time.Millisecond)

	if v := c.get("acct", fetch); !v.Checking {
		t.Error("Checking = false while a probe is in flight")
	}
	close(release)
	time.Sleep(100 * time.Millisecond)

	v := c.get("acct", fetch)
	if v.Checking {
		t.Error("Checking = true after the probe landed")
	}
	if len(v.Windows) != 1 || v.Windows[0].Utilization != 12 {
		t.Errorf("windows = %+v, want the finished reading", v.Windows)
	}
}
