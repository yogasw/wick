package trigger

import (
	"testing"
	"time"
)

// A 6-field schedule puts seconds first; a 5-field one fires only at
// second 0 of a matching minute, as it did when the scheduler ticked
// once a minute.
func TestCronMatchesNow_Seconds(t *testing.T) {
	at := func(min, sec int) time.Time { return time.Date(2026, 1, 5, 10, min, sec, 0, time.UTC) }
	cases := []struct {
		expr string
		now  time.Time
		want bool
	}{
		{"*/30 * * * * *", at(7, 0), true},
		{"*/30 * * * * *", at(7, 30), true},
		{"*/30 * * * * *", at(7, 15), false},
		{"15 */5 * * * *", at(10, 15), true},
		{"15 */5 * * * *", at(11, 15), false},
		{"*/5 * * * *", at(10, 0), true},
		{"*/5 * * * *", at(10, 1), false},
		{"*/5 * * * *", at(11, 0), false},
		{"* * * *", at(10, 0), false},
		{"* * * * * * *", at(10, 0), false},
	}
	for _, tc := range cases {
		if got := cronMatchesNow(tc.expr, tc.now); got != tc.want {
			t.Errorf("cronMatchesNow(%q, %s) = %v, want %v", tc.expr, tc.now.Format("15:04:05"), got, tc.want)
		}
	}
}

// dueSeconds covers every second since the previous call (bounded), and
// a second already evaluated is never evaluated twice.
func TestCronDueSeconds_CatchUpAndNoRepeat(t *testing.T) {
	base := time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC)
	c := NewCronScheduler(nil)

	if got := c.dueSeconds(base); len(got) != 1 || !got[0].Equal(base) {
		t.Fatalf("first call: got %v, want [%s]", got, base)
	}
	if got := c.dueSeconds(base.Add(400 * time.Millisecond)); len(got) != 0 {
		t.Fatalf("same second again: got %v, want none", got)
	}
	got := c.dueSeconds(base.Add(3 * time.Second))
	if len(got) != 3 || !got[0].Equal(base.Add(time.Second)) {
		t.Fatalf("late tick: got %v, want the 3 seconds after %s", got, base)
	}
	got = c.dueSeconds(base.Add(10 * time.Minute))
	if want := int(maxCatchUp/time.Second) + 1; len(got) != want {
		t.Fatalf("long gap: got %d seconds, want catch-up capped at %d", len(got), want)
	}
}
