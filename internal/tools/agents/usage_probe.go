package agents

import (
	"context"
	"errors"
	"math/rand"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/agents/provider/logintty"
)

// The usage endpoint is a shared, rate-limited resource. Every provider
// instance on this host asks the SAME upstream host, from one IP, and
// the providers page re-polls every few seconds with several cards (and
// often several open tabs) behind it. Left alone that is a 429 machine:
// N accounts fire in the same instant, the endpoint refuses, and the
// refusal is retried on the next tick — which earns the next refusal.
//
// Four rules keep it safe, and each one is load-bearing:
//
//  1. ONE reading per account per TTL. Keyed on the account identity
//     (logintty.UsageIdentity), not the config dir, so two folders
//     holding the same login cost one request.
//  2. ONE flight per account. Simultaneous readers wait on the probe
//     already running instead of starting a second one.
//  3. A RANDOMISED gap between any two outbound probes, process-wide,
//     so two providers never hit the endpoint in the same second.
//  4. Failures are CACHED and never retried on their own; a 429's own
//     Retry-After is honoured even against a human pressing Re-check.
//     The single exception is a failure whose CAUSE provably changed:
//     when the account's credentials are rewritten on disk (the CLI
//     refreshed the login), one retry is allowed, because the token we
//     failed with is not the token we would send now. That costs a
//     request only when a login actually changed, so it cannot become
//     polling.
//
// Readings are refreshed in the background, so a slow or paced probe
// never holds the page: the handler serves what the cache knows and the
// badge fills in on a later poll.

const (
	// usageCacheTTL is kept as the cache's nominal freshness for the
	// constructor's sake. It no longer triggers anything: a reading is
	// served until a human replaces it.
	usageCacheTTL = 60 * time.Second

	// usageUnsupportedTTL parks the "this provider type has no usage
	// API" verdict. It is a property of the build, not a condition that
	// clears, so it is re-derived only in case the instance changed.
	usageUnsupportedTTL = 6 * time.Hour

	// Randomised spacing between two outbound probes, process-wide.
	usagePaceMin = 2 * time.Second
	usagePaceMax = 5 * time.Second

	// usageManualMinInterval is the floor between two USER-requested
	// refreshes of one account. A Retry button that fires a request per
	// click is the rate limit's best friend, so the button overrides
	// our own backoff but never this floor — and never a cooldown the
	// server itself asked for.
	usageManualMinInterval = 10 * time.Second
)

// paceGate serialises outbound probes with a randomised gap. Callers
// reserve a slot and sleep for what it hands back; the next caller's
// slot starts a random interval after this one.
type paceGate struct {
	min, max time.Duration
	now      func() time.Time

	mu   sync.Mutex
	next time.Time
}

func newPaceGate(min, max time.Duration) *paceGate {
	return &paceGate{min: min, max: max, now: time.Now}
}

// reserve claims the next slot and returns how long the caller must
// wait before using it. Holding the mutex while reserving is what makes
// two probes racing for the same instant come out spaced instead.
func (g *paceGate) reserve() time.Duration {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	wait := time.Duration(0)
	if g.next.After(now) {
		wait = g.next.Sub(now)
	}
	g.next = now.Add(wait + randomDuration(g.min, g.max))
	return wait
}

// randomDuration picks uniformly in [min, max]. Random, not fixed: a
// constant gap still lets independent hosts/tabs beat in lockstep.
func randomDuration(min, max time.Duration) time.Duration {
	if max <= min {
		return min
	}
	return min + time.Duration(rand.Int63n(int64(max-min)+1))
}

// usageFetch performs the actual remote read.
type usageFetch func() ([]logintty.UsageWindow, error)

// usageEntry is one account's probe state.
type usageEntry struct {
	windows []logintty.UsageWindow
	goodAt  time.Time // when the last successful reading was taken
	err     error     // last failure, kept for display and backoff
	fails   int       // consecutive failures, drives the backoff
	nextAt  time.Time // earliest a manual re-check is accepted
	// attemptedAt is when the last probe finished, successful or not.
	// A manual refresh is measured against this, not against goodAt:
	// failed attempts cost the endpoint just as much as good ones.
	attemptedAt time.Time
	// serverUntil is a cooldown the SERVER asked for (Retry-After).
	// Nothing overrides it — not the backoff reset, not the button.
	serverUntil time.Time
}

// usageCache is the per-account probe cache described above.
type usageCache struct {
	ttl   time.Duration
	gate  *paceGate
	now   func() time.Time
	sleep func(time.Duration)
	run   func(func())

	mu       sync.Mutex
	entries  map[string]*usageEntry
	inflight map[string]chan struct{}
}

func newUsageCache(ttl time.Duration) *usageCache {
	return &usageCache{
		ttl:      ttl,
		gate:     newPaceGate(usagePaceMin, usagePaceMax),
		now:      time.Now,
		sleep:    time.Sleep,
		run:      func(f func()) { go f() },
		entries:  map[string]*usageEntry{},
		inflight: map[string]chan struct{}{},
	}
}

// usageProbes is the process-wide cache behind every usage read.
var usageProbes = newUsageCache(usageCacheTTL)

// usageView is what a caller should display for one account: the
// reading itself plus the provenance the UI shows next to it — when it
// was taken and when the next probe is allowed. Everything the page
// renders about freshness comes from here, so a cached value is never
// passed off as a live one.
type usageView struct {
	Windows []logintty.UsageWindow
	Err     error
	// Known is false while the first reading for this account is still
	// pending — neither data nor a failure yet.
	Known bool
	// FetchedAt is when the served reading was taken (zero when none).
	FetchedAt time.Time
	// NextAt is the earliest moment a MANUAL re-check would be accepted:
	// the 10s floor, or a longer cooldown the endpoint asked for. There
	// is no automatic probe for it to describe.
	NextAt time.Time
	// Checking is true while a probe for this account is in flight, so
	// the UI can say "checking usage…" instead of leaving the user
	// staring at a stale number wondering whether anything is happening.
	Checking bool
}

// Age is how long ago the served reading was taken.
func (v usageView) Age(now time.Time) time.Duration {
	if v.FetchedAt.IsZero() {
		return 0
	}
	return now.Sub(v.FetchedAt)
}

// get serves what the cache knows about key. It never blocks on the
// network, and it does NOT refresh a reading that already exists —
// polling a page must never cost an upstream request. A cold account
// (nothing read yet) gets its first probe here so the page has something
// to show; everything after that is Re-check.
// credsAt is when this account's stored credentials last changed (zero
// when unknown); it is what licenses the one retry described in rule 4.
func (c *usageCache) get(key string, fetch usageFetch, credsAt time.Time) usageView {
	c.schedule(key, fetch, false, credsAt)
	c.mu.Lock()
	e := c.entries[key]
	_, checking := c.inflight[key]
	c.mu.Unlock()
	v := c.serve(e)
	v.Checking = checking
	return v
}

// getWait is get, plus: when nothing is known yet it waits for the
// in-flight probe rather than reporting a blank. This is the path that
// collapses "the same request in the same second" — the second caller
// waits on the first probe's result instead of issuing its own.
//
// Bounded by ctx: a caller that runs out of budget gets the blank and
// the probe keeps going in the background for the next reader.
func (c *usageCache) getWait(ctx context.Context, key string, fetch usageFetch, credsAt time.Time) usageView {
	v := c.get(key, fetch, credsAt)
	if v.Known {
		return v
	}
	c.mu.Lock()
	ch := c.inflight[key]
	c.mu.Unlock()
	if ch == nil {
		return v
	}
	select {
	case <-ch:
	case <-ctx.Done():
		return usageView{}
	}
	c.mu.Lock()
	e := c.entries[key]
	_, checking := c.inflight[key]
	c.mu.Unlock()
	v = c.serve(e)
	v.Checking = checking
	return v
}

// serve turns an entry into what a caller should display: a reading
// that is still within the stale window wins over a later failure, so
// one refused refresh does not blank a card that has good numbers.
func (c *usageCache) serve(e *usageEntry) usageView {
	if e == nil {
		return usageView{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v := usageView{FetchedAt: e.goodAt, NextAt: e.nextAt}
	if !e.goodAt.IsZero() {
		// Served however old it is. Nothing refreshes on its own any
		// more, so a cutoff would simply blank the card and leave the
		// user with less than they had — and the age is on screen, so
		// nobody is being told this number is fresh.
		v.Windows, v.Known = e.windows, true
		return v
	}
	if e.err != nil {
		v.Err, v.Known = e.err, true
		return v
	}
	return usageView{NextAt: e.nextAt}
}

// schedule starts a probe for key. It declines when one is already
// running, and — unless force is set — when the account already has a
// reading: nothing refreshes on its own, because a page poll must never
// cost an upstream request. force comes only from forceRefresh, which
// has already applied the cooldowns. Returns the channel that closes
// when the running probe finishes, or nil when none was started.
func (c *usageCache) schedule(key string, fetch usageFetch, force bool, credsAt time.Time) chan struct{} {
	c.mu.Lock()
	if ch, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		return ch // rule 2: one flight per account
	}
	if e := c.entries[key]; e != nil && !force && !retryOnNewCreds(e, credsAt, c.now()) {
		// An entry exists, so this account has been read at least once.
		// Refreshing it is a human's decision (forceRefresh), never a
		// side effect of someone having a page open — that automatic
		// refresh is exactly what fed the rate limit before.
		c.mu.Unlock()
		return nil
	}
	ch := make(chan struct{})
	c.inflight[key] = ch
	c.mu.Unlock()

	c.run(func() {
		defer func() {
			c.mu.Lock()
			delete(c.inflight, key)
			c.mu.Unlock()
			close(ch)
		}()
		if wait := c.gate.reserve(); wait > 0 {
			c.sleep(wait) // rule 3: randomised gap between providers
		}
		windows, err := fetch()
		c.store(key, windows, err)
	})
	return ch
}

// store records a probe result and decides when the next one is allowed.
func (c *usageCache) store(key string, windows []logintty.UsageWindow, err error) {
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[key]
	if e == nil {
		e = &usageEntry{}
		c.entries[key] = e
	}
	e.attemptedAt = now
	if after := logintty.RetryAfterOf(err); after > 0 {
		e.serverUntil = now.Add(after)
	}
	switch {
	case err == nil:
		e.windows, e.goodAt, e.err, e.fails = windows, now, nil, 0
	case errors.Is(err, logintty.ErrUsageUnsupported):
		e.err, e.windows, e.fails = err, nil, 0
		e.goodAt = time.Time{}
	default:
		e.fails++
		e.err = err
	}
	// One meaning for nextAt: when would a human's Re-check be allowed?
	// That is the manual floor, unless the endpoint asked for longer.
	e.nextAt = now.Add(usageManualMinInterval)
	if e.serverUntil.After(e.nextAt) {
		e.nextAt = e.serverUntil
	}
}

// retryOnNewCreds reports whether a cached FAILURE deserves one more
// probe because the credentials behind it were rewritten since.
//
// Deliberately narrow, so this stays "the cause changed" and never
// becomes "poll again":
//
//   - only an account with no reading at all — a card showing numbers
//     already has something to show, and refreshing it stays a human's
//     call;
//   - only when the credential file is newer than the failed attempt;
//   - never inside a cooldown the SERVER asked for, and never inside
//     the same floor the Re-check button obeys.
//
// Callers hold c.mu.
func retryOnNewCreds(e *usageEntry, credsAt, now time.Time) bool {
	if e.err == nil || !e.goodAt.IsZero() {
		return false
	}
	if credsAt.IsZero() || !credsAt.After(e.attemptedAt) {
		return false
	}
	if e.serverUntil.After(now) {
		return false
	}
	return e.attemptedAt.IsZero() || now.Sub(e.attemptedAt) >= usageManualMinInterval
}

// forceRefresh is the Re-check button: the ONLY way a reading is
// replaced, now that nothing probes on its own.
//
// Two refusals stand in its way, and both exist because a button that
// can be clicked repeatedly is how a rate limit is kept alive:
//
//   - a cooldown the SERVER asked for (Retry-After) is never overridden;
//   - no two probes for one account inside usageManualMinInterval,
//     however many people click.
//
// Returns accepted=false plus how long to wait when it declines, so the
// UI can say "10s more" instead of looking broken. A probe already in
// flight counts as accepted — that click got what it asked for.
func (c *usageCache) forceRefresh(key string, fetch usageFetch) (accepted bool, wait time.Duration) {
	now := c.now()
	c.mu.Lock()
	if _, running := c.inflight[key]; running {
		c.mu.Unlock()
		return true, 0
	}
	if e := c.entries[key]; e != nil {
		if d := e.serverUntil.Sub(now); d > 0 {
			c.mu.Unlock()
			return false, d
		}
		if d := usageManualMinInterval - now.Sub(e.attemptedAt); d > 0 && !e.attemptedAt.IsZero() {
			c.mu.Unlock()
			return false, d
		}
	}
	c.mu.Unlock()
	// force: schedule() declines an account that already has a reading,
	// and this is the one caller allowed to override that — a human
	// asked, and the cooldowns above already said yes.
	c.schedule(key, fetch, true, time.Time{})
	return true, 0
}
