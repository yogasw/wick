package store

import (
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
)

// The trend sums each turn into its bucket, counts a session once per
// bucket it spent in, skips level-only points, and stays inside the window.
func TestAggregateUsageTrend(t *testing.T) {
	layout := config.Layout{BaseDir: t.TempDir()}
	day := time.Date(2026, 3, 10, 0, 0, 0, 0, time.UTC)
	writeLedger(t, layout, "s1", &SessionUsage{
		Providers: map[string]*ProviderUsage{
			"claude/a": {
				Turns: 4, LastAt: day.Add(26 * time.Hour),
				Series: []UsagePoint{
					point(day.Add(-time.Hour), 100, 10, 1), // before the window
					point(day.Add(time.Hour), 100, 10, 1),
					point(day.Add(2*time.Hour), 50, 5, 1),
					{At: day.Add(3 * time.Hour), ContextUsed: 40}, // compaction, not a turn
					point(day.Add(26*time.Hour), 10, 1, 1),
				},
			},
		},
	})
	writeLedger(t, layout, "s2", &SessionUsage{
		Providers: map[string]*ProviderUsage{
			"claude/a": {Turns: 1, LastAt: day.Add(time.Hour), Series: []UsagePoint{point(day.Add(time.Hour), 1, 1, 0)}},
		},
	})
	roll, err := AggregateUsageFiltered(layout, []string{"s1", "s2"}, nil, UsageFilter{
		Since:  day,
		Bucket: func(at time.Time) string { return at.UTC().Format("2006-01-02") },
	})
	if err != nil {
		t.Fatal(err)
	}
	first := roll.Trend["2026-03-10"]
	if first == nil || first.Turns != 3 || first.Sessions != 2 || first.Totals.Input != 151 {
		t.Fatalf("2026-03-10 = %+v", first)
	}
	second := roll.Trend["2026-03-11"]
	if second == nil || second.Turns != 1 || second.Sessions != 1 {
		t.Fatalf("2026-03-11 = %+v", second)
	}
	if roll.Trend["2026-03-09"] != nil {
		t.Error("a turn before the window landed in the trend")
	}

	plain, _ := AggregateUsageFiltered(layout, []string{"s1"}, nil, UsageFilter{Since: day})
	if plain.Trend != nil {
		t.Error("trend built without a Bucket")
	}
}
