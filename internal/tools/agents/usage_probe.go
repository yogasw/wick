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
//  4. Failures are CACHED with backoff, and a 429's own Retry-After is
//     honoured. Nothing retries a rate limit on the next page poll.
//
// Readings are refreshed in the background, so a slow or paced probe
// never holds the page: the handler serves what the cache knows and the
// badge fills in on a later poll.

const (
	// usageCacheTTL is how long a good reading is reused. The numbers
	// are rolling-window utilization percentages that move over
	// minutes, so a minute keeps the badges honest while making page
	// refreshes free.
	usageCacheTTL = 60 * time.Second

	// usageStaleWindow is how long a last-good reading keeps being
	// shown after refreshes start failing. Slightly stale percentages
	// beat "usage unavailable" — and it means a transient 429 is
	// invisible to the user rather than blanking the card.
	usageStaleWindow = 30 * time.Minute

	// Backoff for ordinary failures (timeout, DNS, 5xx).
	usageErrBackoffMin = 30 * time.Second
	usageErrBackoffMax = 10 * time.Minute

	// Backoff for a 429 with no Retry-After. Deliberately much longer:
	// the endpoint has already told us we are asking too often.
	usageRateLimitBackoffMin = 2 * time.Minute
	usageRateLimitBackoffMax = 15 * time.Minute

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
	nextAt  time.Time // earliest allowed next probe
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
	ttl      time.Duration
	staleFor time.Duration
	gate     *paceGate
	now      func() time.Time
	sleep    func(time.Duration)
	run      func(func())

	mu       sync.Mutex
	entries  map[string]*usageEntry
	inflight map[string]chan struct{}
}

func newUsageCache(ttl time.Duration) *usageCache {
	return &usageCache{
		ttl:      ttl,
		staleFor: usageStaleWindow,
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
	// NextAt is the earliest moment the next probe may run: the TTL for
	// a good reading, the backoff for a failed one.
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

// get serves what the cache knows about key, scheduling a background
// refresh when one is due. It never blocks on the network.
func (c *usageCache) get(key string, fetch usageFetch) usageView {
	c.schedule(key, fetch)
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
func (c *usageCache) getWait(ctx context.Context, key string, fetch usageFetch) usageView {
	v := c.get(key, fetch)
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
	if !e.goodAt.IsZero() && c.now().Sub(e.goodAt) <= c.staleFor {
		v.Windows, v.Known = e.windows, true
		return v
	}
	if e.err != nil {
		v.Err, v.Known = e.err, true
		return v
	}
	return usageView{NextAt: e.nextAt}
}

// schedule starts a refresh for key unless one is already running or
// the entry is still inside its TTL / backoff. Returns the channel that
// closes when the running probe finishes, or nil when none was needed.
func (c *usageCache) schedule(key string, fetch usageFetch) chan struct{} {
	c.mu.Lock()
	if ch, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		return ch // rule 2: one flight per account
	}
	if e := c.entries[key]; e != nil && c.now().Before(e.nextAt) {
		c.mu.Unlock()
		return nil // rule 1 & 4: inside the TTL, or still cooling down
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
		e.nextAt = now.Add(c.ttl)
	case errors.Is(err, logintty.ErrUsageUnsupported):
		e.err, e.windows, e.fails = err, nil, 0
		e.goodAt = time.Time{}
		e.nextAt = now.Add(usageUnsupportedTTL)
	default:
		e.fails++
		e.err = err
		e.nextAt = now.Add(usageBackoff(err, e.fails))
	}
}

// forceRefresh is the Retry button: it drops OUR backoff for this
// account and asks for a probe now.
//
// Two things it deliberately does not do, because a button that can be
// clicked repeatedly is exactly how a rate limit is kept alive:
//
//   - It never overrides a cooldown the server asked for (Retry-After).
//     Retrying inside that window earns the next 429 by design.
//   - It never probes twice inside usageManualMinInterval, however many
//     people click.
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
		e.nextAt = now // due now: schedule() will take it
	}
	c.mu.Unlock()
	c.schedule(key, fetch)
	return true, 0
}

// usageBackoff is the cooldown after a failed probe: the server's own
// Retry-After when it sent one, otherwise exponential from the floor
// for this failure kind, with jitter so several accounts that failed
// together do not all come back in the same second.
func usageBackoff(err error, fails int) time.Duration {
	if after := logintty.RetryAfterOf(err); after > 0 {
		return after + randomDuration(usagePaceMin, usagePaceMax)
	}
	min, max := usageErrBackoffMin, usageErrBackoffMax
	if logintty.IsRateLimited(err) {
		min, max = usageRateLimitBackoffMin, usageRateLimitBackoffMax
	}
	d := min
	for i := 1; i < fails && d < max; i++ {
		d *= 2
	}
	if d > max {
		d = max
	}
	return d + randomDuration(0, d/4)
}
