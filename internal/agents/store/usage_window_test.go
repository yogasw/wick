package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/storage"
)

// writeLedger drops a usage.json for one session, the way a finished
// turn would have.
func writeLedger(t *testing.T, layout config.Layout, id string, su *SessionUsage) {
	t.Helper()
	path := layout.SessionUsage(id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := storage.WriteJSON(path, su); err != nil {
		t.Fatal(err)
	}
}

func point(at time.Time, in, out int, cost float64) UsagePoint {
	return UsagePoint{At: at, Input: in, Output: out, CostUSD: cost, ContextUsed: in + out}
}

// The whole point of a window: yesterday's spend must not show up in
// "today". An all-time-only ledger cannot answer the question people
// actually ask ("what is this costing us now"), because a number that
// only grows never shows that anything changed.
func TestAggregateUsageSinceExcludesOlderTurns(t *testing.T) {
	layout := config.Layout{BaseDir: t.TempDir()}
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	writeLedger(t, layout, "s1", &SessionUsage{
		Providers: map[string]*ProviderUsage{
			"claude/a": {
				UsageTotals: UsageTotals{Input: 300, Output: 30, CostUSD: 6},
				Turns:       3,
				LastAt:      now,
				Series: []UsagePoint{
					point(midnight.Add(-30*time.Hour), 100, 10, 2), // yesterday
					point(midnight.Add(2*time.Hour), 100, 10, 2),   // today
					point(now, 100, 10, 2),                         // today
				},
			},
		},
		Totals: UsageTotals{Input: 300, Output: 30, CostUSD: 6},
		Turns:  3,
	})

	all, err := AggregateUsage(layout, []string{"s1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if all.Totals.CostUSD != 6 || all.Turns != 3 {
		t.Fatalf("all time = %+v turns=%d, want the exact stored totals", all.Totals, all.Turns)
	}

	today, err := AggregateUsageSince(layout, []string{"s1"}, nil, midnight)
	if err != nil {
		t.Fatal(err)
	}
	if today.Totals.CostUSD != 4 || today.Totals.Input != 200 || today.Turns != 2 {
		t.Fatalf("today = %+v turns=%d, want only the two turns after midnight", today.Totals, today.Turns)
	}
	if today.Partial {
		t.Error("a complete trail must not be reported as partial")
	}
}

// A session that spent nothing inside the window is not in the window.
// Listing it would put a row of zeroes under "Used in" for every session
// that ever touched the provider.
func TestAggregateUsageSinceDropsSessionsOutsideTheWindow(t *testing.T) {
	layout := config.Layout{BaseDir: t.TempDir()}
	now := time.Now()
	old := now.Add(-90 * 24 * time.Hour)

	writeLedger(t, layout, "stale", &SessionUsage{
		Providers: map[string]*ProviderUsage{
			"claude/a": {
				UsageTotals: UsageTotals{Input: 10, Output: 1, CostUSD: 1},
				Turns:       1, LastAt: old,
				Series: []UsagePoint{point(old, 10, 1, 1)},
			},
		},
		Turns: 1,
	})
	writeLedger(t, layout, "live", &SessionUsage{
		Providers: map[string]*ProviderUsage{
			"claude/a": {
				UsageTotals: UsageTotals{Input: 20, Output: 2, CostUSD: 2},
				Turns:       1, LastAt: now,
				Series: []UsagePoint{point(now, 20, 2, 2)},
			},
		},
		Turns: 1,
	})

	roll, err := AggregateUsageSince(layout, []string{"stale", "live"}, nil, now.Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if roll.Sessions != 1 {
		t.Fatalf("sessions = %d, want only the one that spent inside the window", roll.Sessions)
	}
	uses := roll.ProviderSessions["claude/a"]
	if len(uses) != 1 || uses[0].ID != "live" {
		t.Fatalf("provider sessions = %+v, want only \"live\"", uses)
	}
	if uses[0].Totals.CostUSD != 2 {
		t.Fatalf("row totals = %+v, want the windowed spend", uses[0].Totals)
	}
}

// The per-turn trail is capped, so a long session cannot always answer a
// wide window. Saying so is the difference between "$4 today" and "at
// least $4" — and a report that does not say it is one somebody quotes
// as exact.
func TestAggregateUsageSinceFlagsATruncatedTrail(t *testing.T) {
	layout := config.Layout{BaseDir: t.TempDir()}
	now := time.Now()
	series := make([]UsagePoint, 0, UsageSeriesMax)
	for i := 0; i < UsageSeriesMax; i++ {
		series = append(series, point(now.Add(-time.Duration(UsageSeriesMax-i)*time.Minute), 1, 1, 0.01))
	}
	writeLedger(t, layout, "long", &SessionUsage{
		Providers: map[string]*ProviderUsage{
			"claude/a": {
				UsageTotals: UsageTotals{Input: 9999, Output: 9999, CostUSD: 99},
				Turns:       9999, LastAt: now, Series: series,
			},
		},
		Turns: 9999,
	})

	roll, err := AggregateUsageSince(layout, []string{"long"}, nil, now.Add(-30*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if !roll.Partial || roll.PartialSessions != 1 {
		t.Fatalf("partial=%v sessions=%d, want the truncated trail reported", roll.Partial, roll.PartialSessions)
	}
	// All time is still exact — it never touches the trail.
	exact, err := AggregateUsage(layout, []string{"long"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if exact.Partial || exact.Totals.CostUSD != 99 {
		t.Fatalf("all time = %+v partial=%v, want the exact stored totals", exact.Totals, exact.Partial)
	}
}

// Compaction writes a level-only point. Counting it as a turn would
// inflate the turn count of every compacted session — and those are
// exactly the long sessions people look at.
func TestTotalsSinceIgnoresCompactionPoints(t *testing.T) {
	now := time.Now()
	p := &ProviderUsage{
		Turns: 1, LastAt: now,
		Series: []UsagePoint{
			point(now.Add(-time.Hour), 100, 10, 1),
			{At: now, ContextUsed: 5_000}, // a compaction, no flows
		},
	}
	totals, turns, complete := p.TotalsSince(now.Add(-24 * time.Hour))
	if turns != 1 {
		t.Fatalf("turns = %d, want 1 (the compaction point is not a turn)", turns)
	}
	if totals.Input != 100 || totals.CostUSD != 1 {
		t.Fatalf("totals = %+v, want only the real turn's flows", totals)
	}
	if !complete {
		t.Error("a short trail reaches back past the window; it is complete")
	}
}

// Newest first: "where is this provider used" is asked about what is
// running now, and a list sorted by id answers a question nobody has.
func TestProviderSessionsAreNewestFirst(t *testing.T) {
	layout := config.Layout{BaseDir: t.TempDir()}
	now := time.Now()
	for i, id := range []string{"aaa", "zzz"} {
		at := now.Add(time.Duration(i) * time.Hour) // zzz is newer
		writeLedger(t, layout, id, &SessionUsage{
			Providers: map[string]*ProviderUsage{
				"claude/a": {
					UsageTotals: UsageTotals{Input: 10, Output: 1},
					Turns:       1, LastAt: at,
					Series: []UsagePoint{point(at, 10, 1, 0)},
				},
			},
			Turns: 1,
		})
	}
	roll, err := AggregateUsage(layout, []string{"aaa", "zzz"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	uses := roll.ProviderSessions["claude/a"]
	if len(uses) != 2 || uses[0].ID != "zzz" {
		t.Fatalf("order = %+v, want the most recently used first", uses)
	}
}
