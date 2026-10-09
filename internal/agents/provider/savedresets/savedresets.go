// Package savedresets is the provider-agnostic base for saved rate-limit
// resets: one-time credits some accounts hold that refill their usage
// windows on demand (Codex's banked resets, Claude Code's /limit-reset).
// wick only READS them, so the usage surfaces can say how many an account
// has and when they lapse; spending one is left to the CLI.
//
// The package holds the normalised types, the reader registry and a few
// helpers, and depends on nothing but the standard library. Each provider
// keeps its own reader in its own file and registers it under its type
// name; callers look the reader up and never switch on the type. A new
// provider, or a provider shipped as a plugin, imports this package alone.
package savedresets

import (
	"errors"
	"sort"
	"sync"
	"time"
)

// ErrUnsupported is returned by Read for a type with no registered reader.
var ErrUnsupported = errors.New("saved resets not supported for this provider type")

// SavedResets is one account's saved-reset balance.
type SavedResets struct {
	// Supported is true when the account answered at all. An account the
	// provider does not offer resets to is still Supported, with
	// Available 0 and a Note saying so.
	Supported bool
	// Available is how many resets can still be spent.
	Available int
	// Total is how many were granted, when the provider says (0 = unknown).
	Total int
	// Items lists the individual resets, soonest expiry first.
	Items []Reset
	// CooldownUntil is when the next reset becomes usable, when the
	// provider spaces them out. Zero when there is no cooldown.
	CooldownUntil time.Time
	// Note replaces the count when there is nothing to offer, e.g.
	// "Not available for this account."
	Note string
	// Hint is the provider's own one-line explanation of what a reset
	// does and where to spend it, shown under the list.
	Hint string
}

// Reset is one spendable reset.
type Reset struct {
	ID        string
	Label     string
	ExpiresAt time.Time
	StartsAt  time.Time
	UsableNow bool
	// RequiresLimit marks a reset that can only be spent while the
	// account is AT a limit; UsableNow is then false until it is.
	RequiresLimit bool
}

// Reader reads one instance's saved resets from its env. It runs inside
// the caller's paced usage probe, right after the usage reading, so it
// must not loop or retry on its own.
type Reader func(env []string) (*SavedResets, error)

var (
	mu      sync.RWMutex
	readers = map[string]Reader{}
)

// Register installs the reader for one provider type name, replacing any
// earlier one. A nil reader unregisters the type.
func Register(providerType string, r Reader) {
	mu.Lock()
	defer mu.Unlock()
	if r == nil {
		delete(readers, providerType)
		return
	}
	readers[providerType] = r
}

// ReaderFor looks up the reader registered for providerType.
func ReaderFor(providerType string) (Reader, bool) {
	mu.RLock()
	defer mu.RUnlock()
	r, ok := readers[providerType]
	return r, ok
}

// Read runs the reader for providerType, or returns ErrUnsupported.
func Read(providerType string, env []string) (*SavedResets, error) {
	r, ok := ReaderFor(providerType)
	if !ok {
		return nil, ErrUnsupported
	}
	return r(env)
}

// SortItems orders items soonest expiry first; undated ones last.
func SortItems(items []Reset) {
	sort.SliceStable(items, func(i, j int) bool {
		a, b := items[i].ExpiresAt, items[j].ExpiresAt
		switch {
		case a.IsZero():
			return false
		case b.IsZero():
			return true
		}
		return a.Before(b)
	})
}

// SoonestExpiry is the earliest dated item's expiry, zero when none is dated.
func SoonestExpiry(r *SavedResets) time.Time {
	var best time.Time
	if r == nil {
		return best
	}
	for _, it := range r.Items {
		if !it.ExpiresAt.IsZero() && (best.IsZero() || it.ExpiresAt.Before(best)) {
			best = it.ExpiresAt
		}
	}
	return best
}

// ParseTime reads an RFC 3339 string or a unix timestamp in seconds or
// milliseconds, as providers send either; anything else is the zero time.
func ParseTime(v any) time.Time {
	switch x := v.(type) {
	case string:
		if ts, err := time.Parse(time.RFC3339, x); err == nil {
			return ts
		}
	case float64:
		switch {
		case x > 1e12:
			return time.UnixMilli(int64(x))
		case x > 0:
			return time.Unix(int64(x), 0)
		}
	}
	return time.Time{}
}
