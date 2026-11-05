package usagereport

import (
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/store"
)

// seeded returns a builder whose roll-up for w is already cached, so the
// report can be tested without a session tree on disk.
func seeded(w Window, roll store.UsageRollup) *Builder {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	b := New(config.Layout{}, func(id string) string { return map[string]string{"u1": "Alice"}[id] })
	b.now = func() time.Time { return now }
	b.cache[w.CacheKey()+Scope{}.Key()] = cacheEntry{at: now, roll: roll}
	return b
}

// A conversation that switched provider is ONE row: the question is which
// conversation spent it, not which door it went through.
func TestSessionsMergeProvidersAndSortByTokens(t *testing.T) {
	t1 := time.Date(2026, 3, 10, 9, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 3, 10, 11, 0, 0, 0, time.UTC)
	roll := store.UsageRollup{
		Totals: store.UsageTotals{Input: 1000, Output: 100},
		Turns:  12,
		ProviderSessions: map[string][]store.SessionUse{
			"claude/a": {
				{ID: "s-small", Totals: store.UsageTotals{Input: 100, Output: 10}, Turns: 2, LastAt: t2},
				{ID: "s-big", Totals: store.UsageTotals{Input: 500, Output: 50}, Turns: 5, LastAt: t1},
			},
			"claude/b": {
				{ID: "s-big", Totals: store.UsageTotals{Input: 400, Output: 40}, Turns: 5, LastAt: t2},
			},
		},
		SessionTagsByID: map[string]store.SessionTags{
			"s-big":   {UserID: "u1", Label: "heavy one", Channel: "slack"},
			"s-small": {UserID: "u2", Label: "light one"},
		},
	}
	w := ParseWindow("all", time.Now())
	rep, err := seeded(w, roll).Sessions(w, Scope{}, false, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Sort != SortTokens || rep.Total != 2 || len(rep.Sessions) != 2 {
		t.Fatalf("report = %+v, want 2 rows sorted by tokens", rep)
	}
	big := rep.Sessions[0]
	if big.ID != "s-big" || big.Totals.Total != 990 || big.Turns != 10 {
		t.Fatalf("row 0 = %+v, want s-big merged across both providers", big)
	}
	if len(big.Providers) != 2 || big.UserName != "Alice" || big.Channel != "slack" {
		t.Fatalf("row 0 = %+v, want both providers, the user name and the channel", big)
	}
	if big.LastAt != t2.Format(time.RFC3339) {
		t.Fatalf("last_at = %s, want the later of the two", big.LastAt)
	}
	if big.Share != 90 {
		t.Fatalf("share = %v, want 90", big.Share)
	}

	rep, _ = seeded(w, roll).Sessions(w, Scope{}, false, SortRecent, 1)
	if rep.Total != 2 || len(rep.Sessions) != 1 {
		t.Fatalf("limit 1: total=%d rows=%d, want 2 and 1", rep.Total, len(rep.Sessions))
	}
}

// A rate-limit window starts mid-day: a bound with a clock must stay that
// instant instead of being widened to the whole day.
func TestWindowFromQueryKeepsTheClock(t *testing.T) {
	now := time.Date(2026, 3, 10, 18, 0, 0, 0, time.UTC)
	w := WindowFromQuery("", "2026-03-10T13:30", "2026-03-10T18:30", now)
	if w.Since != time.Date(2026, 3, 10, 13, 30, 0, 0, time.UTC) || w.Until != time.Date(2026, 3, 10, 18, 30, 0, 0, time.UTC) {
		t.Fatalf("window = %+v, want 13:30–18:30", w)
	}
	// A date alone still means whole days.
	w = WindowFromQuery("", "2026-03-09", "2026-03-10", now)
	if w.Since != time.Date(2026, 3, 9, 0, 0, 0, 0, time.UTC) || w.Until.Hour() != 23 {
		t.Fatalf("window = %+v, want whole days", w)
	}
	// An explicit zone wins over the server's.
	w = WindowFromQuery("", "2026-03-10T20:00:00+07:00", "", now)
	if !w.Since.Equal(time.Date(2026, 3, 10, 13, 0, 0, 0, time.UTC)) {
		t.Fatalf("since = %v, want 13:00 UTC", w.Since)
	}
}
