package logintty

import (
	"testing"
	"time"
)

// fakeClock returns a now func plus an advance func.
func fakeClock(start time.Time) (func() time.Time, func(time.Duration)) {
	cur := start
	return func() time.Time { return cur }, func(d time.Duration) { cur = cur.Add(d) }
}

func TestCountdownRemainingTicksDown(t *testing.T) {
	now, advance := fakeClock(time.Unix(1000, 0))
	c := NewCountdown(5*time.Minute, 30*time.Minute, now)
	if got := c.Remaining(); got != 5*time.Minute {
		t.Fatalf("Remaining = %v, want 5m", got)
	}
	advance(2 * time.Minute)
	if got := c.Remaining(); got != 3*time.Minute {
		t.Fatalf("Remaining = %v, want 3m", got)
	}
	if c.Expired() {
		t.Fatal("not expired yet")
	}
}

func TestCountdownExpires(t *testing.T) {
	now, advance := fakeClock(time.Unix(1000, 0))
	c := NewCountdown(5*time.Minute, 30*time.Minute, now)
	advance(5*time.Minute + time.Second)
	if !c.Expired() {
		t.Fatal("must be expired")
	}
	if got := c.Remaining(); got != 0 {
		t.Fatalf("Remaining = %v, want 0 (never negative)", got)
	}
}

func TestCountdownExtendAddsStep(t *testing.T) {
	now, advance := fakeClock(time.Unix(1000, 0))
	c := NewCountdown(5*time.Minute, 30*time.Minute, now)
	advance(4 * time.Minute)
	if !c.Extend(5 * time.Minute) {
		t.Fatal("extend must succeed under cap")
	}
	if got := c.Remaining(); got != 6*time.Minute {
		t.Fatalf("Remaining = %v, want 6m (1m left + 5m)", got)
	}
}

func TestCountdownExtendClampsAtCap(t *testing.T) {
	now, _ := fakeClock(time.Unix(1000, 0))
	c := NewCountdown(5*time.Minute, 30*time.Minute, now)
	for i := 0; i < 10; i++ {
		c.Extend(5 * time.Minute)
	}
	// Deadline may never exceed start+30m.
	if got := c.Remaining(); got != 30*time.Minute {
		t.Fatalf("Remaining = %v, want 30m cap", got)
	}
	if c.Extend(5 * time.Minute) {
		t.Fatal("extend at cap must return false")
	}
}
