package agents

import (
	"math"
	"sort"
	"time"

	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/logintty"
)

// A running claude CLI reports the account's rate-limit windows on its
// own (stream-json rate_limit_event) whenever a level or a reset moves.
// Those reports feed the same per-account cache the usage probe fills,
// so a card stays current while the account works, without a single
// request to the usage endpoint.

// usageObservePersistEvery bounds how long an unchanged observed reading
// may go without reaching the usage file. Below provider.UsageFreshFor,
// so an account in use never looks stale to the failover side.
const usageObservePersistEvery = 5 * time.Minute

func init() { provider.SetRateLimitObserver(ObserveRateLimit) }

// ObserveRateLimit records one rate-limit report of ins's account in the
// process-wide usage cache, under the key every usage read of ins uses.
func ObserveRateLimit(ins provider.Instance, info *event.RateLimitInfo) {
	ws := observedWindows(info)
	if len(ws) == 0 {
		return
	}
	usageProbes.observe(logintty.UsageIdentity(ins.Type, provider.AccountEnv(ins)), ws)
}

// observedWindows turns a report into usage windows, well-known keys
// first (the order the probe serves them in).
func observedWindows(info *event.RateLimitInfo) []logintty.UsageWindow {
	if info == nil {
		return nil
	}
	ws := make([]logintty.UsageWindow, 0, len(info.Windows))
	for k, w := range info.Windows {
		ws = append(ws, logintty.UsageWindow{Key: k, Utilization: w.Utilization, ResetsAt: w.ResetsAt})
	}
	sort.Slice(ws, func(i, j int) bool {
		oi, iok := observedWindowOrder[ws[i].Key]
		oj, jok := observedWindowOrder[ws[j].Key]
		switch {
		case iok && jok:
			return oi < oj
		case iok != jok:
			return iok
		default:
			return ws[i].Key < ws[j].Key
		}
	})
	return ws
}

var observedWindowOrder = map[string]int{"five_hour": 0, "seven_day": 1, "seven_day_opus": 2}

// observe stores windows the CLI reported as the account's current
// reading. Unlike a probe it is free, so none of the pacing applies, and
// it leaves the probe bookkeeping alone: a cooldown the server asked for
// (serverUntil) and the Re-check floor (attemptedAt, nextAt) still hold
// for the next real probe. Saved resets are not in the report and are
// kept as they are.
//
// A window the report does not carry (seven_day_opus, say) keeps its
// last value, stamped with when that value was read, so it is not passed
// off as fresh.
//
// The usage file is rewritten only when something a reader would see
// changed — a whole-percent level, a reset, the account coming back from
// a failure — or usageObservePersistEvery has passed; a long turn
// otherwise writes it on every response. Reports whether it wrote.
func (c *usageCache) observe(key string, observed []logintty.UsageWindow) bool {
	if len(observed) == 0 {
		return false
	}
	now := c.now()
	var persist func()
	defer func() {
		if persist != nil {
			persist()
		}
	}()
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entryLocked(key)
	if e == nil {
		e = &usageEntry{}
		c.entries[key] = e
	}
	merged, changed := mergeObserved(e.windows, e.goodAt, observed)
	if e.goodAt.IsZero() || e.err != nil || e.fails != 0 {
		changed = true
	}
	e.windows, e.goodAt, e.err, e.fails = merged, now, nil, 0
	// A live reading supersedes the file's: no post-restart probe needed.
	e.fromDisk = false
	if !changed && !e.persistedAt.IsZero() && now.Sub(e.persistedAt) < usageObservePersistEvery {
		return false
	}
	persist = c.persistLocked(key, nil)
	if persist == nil {
		return false
	}
	e.persistedAt = now
	return true
}

// mergeObserved lays observed windows over the previous reading (read at
// prevAt). changed reports a difference a reader would notice.
func mergeObserved(prev []logintty.UsageWindow, prevAt time.Time, observed []logintty.UsageWindow) ([]logintty.UsageWindow, bool) {
	byKey := make(map[string]logintty.UsageWindow, len(observed))
	for _, w := range observed {
		byKey[w.Key] = w
	}
	out := make([]logintty.UsageWindow, 0, len(prev)+len(observed))
	changed := false
	for _, old := range prev {
		w, ok := byKey[old.Key]
		if !ok || old.Account != "" || old.Error != "" {
			if old.ObservedAt.IsZero() {
				old.ObservedAt = prevAt
			}
			out = append(out, old)
			continue
		}
		delete(byKey, old.Key)
		if math.Round(old.Utilization) != math.Round(w.Utilization) ||
			!old.ResetsAt.Truncate(time.Second).Equal(w.ResetsAt.Truncate(time.Second)) {
			changed = true
		}
		out = append(out, w)
	}
	for _, w := range observed {
		if _, fresh := byKey[w.Key]; fresh {
			out = append(out, w)
			changed = true
		}
	}
	return out, changed
}
