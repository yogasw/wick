package schedule

import (
	"testing"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

func TestParseWhen_OneShot(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	sp, err := ParseWhen("+2h", "", "", now)
	if err != nil {
		t.Fatalf("offset: %v", err)
	}
	if sp.Recurring || !sp.FirstRunAt.Equal(now.Add(2*time.Hour)) {
		t.Fatalf("bad one-shot spec: %+v", sp)
	}

	sp, err = ParseWhen("2026-07-09T12:40:00Z", "", "", now)
	if err != nil || sp.Recurring || !sp.FirstRunAt.Equal(time.Date(2026, 7, 9, 12, 40, 0, 0, time.UTC)) {
		t.Fatalf("absolute one-shot: %+v err=%v", sp, err)
	}

	// Forgiving: a bare duration in run_at (no "+") means "from now".
	for _, in := range []string{"1m", "90m", "2h", "1d"} {
		sp, err := ParseWhen(in, "", "", now)
		if err != nil {
			t.Fatalf("bare duration %q: %v", in, err)
		}
		if sp.Recurring || !sp.FirstRunAt.After(now) {
			t.Fatalf("bare duration %q → %+v (want future one-shot)", in, sp)
		}
	}
}

func TestParseWhen_Interval(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	sp, err := ParseWhen("", "5m", "", now)
	if err != nil {
		t.Fatalf("interval: %v", err)
	}
	if !sp.Recurring || sp.IntervalMs != (5*time.Minute).Milliseconds() {
		t.Fatalf("bad interval spec: %+v", sp)
	}
	// First fire defaults to now+interval.
	if !sp.FirstRunAt.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("first fire = %v", sp.FirstRunAt)
	}
	// "1d" day unit + compound.
	sp, err = ParseWhen("", "1d", "", now)
	if err != nil || sp.IntervalMs != (24*time.Hour).Milliseconds() {
		t.Fatalf("1d interval: %+v err=%v", sp, err)
	}
}

func TestParseWhen_Cron(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC) // Thursday
	sp, err := ParseWhen("", "", "0 9 * * *", now)      // daily 09:00
	if err != nil {
		t.Fatalf("cron: %v", err)
	}
	if !sp.Recurring || sp.Cron != "0 9 * * *" {
		t.Fatalf("bad cron spec: %+v", sp)
	}
	// Next 09:00 after 12:00 today is tomorrow 09:00.
	want := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
	if !sp.FirstRunAt.Equal(want) {
		t.Fatalf("cron first fire = %v, want %v", sp.FirstRunAt, want)
	}
}

func TestParseWhen_Rejects(t *testing.T) {
	now := time.Now()
	bad := [][3]string{
		{"", "", ""},                 // nothing
		{"", "5m", "0 9 * * *"},      // both interval and cron
		{"", "notaduration", ""},     // bad interval
		{"", "", "0 9 * *"},          // 4-field cron
		{"2020-01-01T00:00:00Z", "", ""}, // past
	}
	for _, c := range bad {
		if _, err := ParseWhen(c[0], c[1], c[2], now); err == nil {
			t.Fatalf("ParseWhen(%q,%q,%q): expected error", c[0], c[1], c[2])
		}
	}
}

func TestAdvance_StopConditions(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	base := entity.ScheduledMessage{Kind: entity.ScheduledKindRecurring, IntervalMs: (time.Hour).Milliseconds()}

	// Normal advance.
	next, err := advance(base, now, 1)
	if err != nil || !next.Equal(now.Add(time.Hour)) {
		t.Fatalf("advance: %v err=%v", next, err)
	}

	// max_runs reached → zero (finish).
	capped := base
	capped.MaxRuns = 3
	if n, _ := advance(capped, now, 3); !n.IsZero() {
		t.Fatalf("max_runs not enforced: %v", n)
	}

	// ends_at passed → zero.
	ended := base
	end := now.Add(30 * time.Minute)
	ended.EndsAt = &end
	if n, _ := advance(ended, now, 1); !n.IsZero() {
		t.Fatalf("ends_at not enforced: %v", n)
	}
}

func TestNextCronAfter_Weekly(t *testing.T) {
	// Monday 09:00; from a Thursday it should land on the next Monday.
	from := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC) // Thu
	got, err := nextCronAfter("0 9 * * 1", from)
	if err != nil {
		t.Fatalf("cron: %v", err)
	}
	if got.Weekday() != time.Monday || got.Hour() != 9 || got.Minute() != 0 {
		t.Fatalf("next Monday 09:00 = %v", got)
	}
}
