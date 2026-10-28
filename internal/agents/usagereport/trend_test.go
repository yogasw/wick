package usagereport

import (
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/store"
)

// A short window is cut into hours, a longer one into days, and the quiet
// buckets are there as zeros: a gap in use must read as a gap, not as two
// neighbouring bars.
func TestBuildTrendGrainAndGaps(t *testing.T) {
	loc := time.FixedZone("T", 7*3600)
	now := time.Date(2026, 3, 10, 14, 30, 0, 0, loc)

	today := Window{Since: time.Date(2026, 3, 10, 0, 0, 0, 0, loc)}
	roll := store.UsageRollup{Trend: map[string]*store.TrendBucket{
		"2026-03-10 09:00": {Totals: store.UsageTotals{Input: 10, CacheRead: 90}, Turns: 2, Sessions: 1},
	}}
	pts, unit := buildTrend(roll, today, now)
	if unit != "hour" || len(pts) != 15 {
		t.Fatalf("today: unit %q, %d points, want hour / 15", unit, len(pts))
	}
	if pts[9].Key != "2026-03-10 09:00" || pts[9].Totals.Total != 100 || pts[9].Turns != 2 {
		t.Errorf("09:00 = %+v", pts[9])
	}
	if pts[8].Turns != 0 || pts[8].Totals.Total != 0 {
		t.Errorf("quiet hour not zero: %+v", pts[8])
	}

	week := Window{Since: time.Date(2026, 3, 4, 0, 0, 0, 0, loc)}
	pts, unit = buildTrend(store.UsageRollup{}, week, now)
	if unit != "day" || len(pts) != 7 || pts[0].Key != "2026-03-04" || pts[6].Key != "2026-03-10" {
		t.Fatalf("week: unit %q, %d points, %v…", unit, len(pts), pts)
	}
}

// All time starts where the data starts, and folds into months once a
// bar per day would be noise.
func TestBuildTrendAllTimeFoldsToMonths(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, loc)
	recent := store.UsageRollup{Trend: map[string]*store.TrendBucket{
		"2026-03-08": {Turns: 1, Sessions: 1},
	}}
	pts, unit := buildTrend(recent, Window{}, now)
	if unit != "day" || len(pts) != 3 || pts[0].Key != "2026-03-08" {
		t.Fatalf("all time, recent: unit %q, %v", unit, pts)
	}

	old := store.UsageRollup{Trend: map[string]*store.TrendBucket{
		"2024-01-15": {Totals: store.UsageTotals{Output: 5}, Turns: 1, Sessions: 1},
		"2024-01-20": {Totals: store.UsageTotals{Output: 5}, Turns: 1, Sessions: 1},
	}}
	pts, unit = buildTrend(old, Window{}, now)
	if unit != "month" || pts[0].Key != "2024-01" || pts[0].Turns != 2 || pts[0].Totals.Output != 10 {
		t.Fatalf("all time, old: unit %q, first %+v", unit, pts[0])
	}
	if pts[len(pts)-1].Key != "2026-03" {
		t.Errorf("last month = %q", pts[len(pts)-1].Key)
	}
}

// Nothing recorded and no window start: no trend at all, rather than one
// zero bar for today pretending to be history.
func TestBuildTrendEmptyAllTime(t *testing.T) {
	if pts, _ := buildTrend(store.UsageRollup{}, Window{}, time.Now()); pts != nil {
		t.Errorf("empty all-time trend = %v", pts)
	}
}
