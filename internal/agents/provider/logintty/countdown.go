package logintty

import (
	"sync"
	"time"
)

// Countdown is the kill-timer for one login TTY session: it starts at
// a default duration, can be extended in steps, and is capped at a max
// total lifetime measured from start. All methods are safe for
// concurrent use.
type Countdown struct {
	mu       sync.Mutex
	now      func() time.Time
	deadline time.Time
	max      time.Time
}

// NewCountdown starts a countdown of d, capped at max total lifetime
// from now. now is injectable for tests; nil = time.Now.
func NewCountdown(d, max time.Duration, now func() time.Time) *Countdown {
	if now == nil {
		now = time.Now
	}
	start := now()
	return &Countdown{
		now:      now,
		deadline: start.Add(d),
		max:      start.Add(max),
	}
}

// Extend pushes the deadline out by step, clamped to the max lifetime.
// Returns false when the deadline is already at the cap (nothing
// changed).
func (c *Countdown) Extend(step time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.deadline.Before(c.max) {
		return false
	}
	next := c.deadline.Add(step)
	if next.After(c.max) {
		next = c.max
	}
	c.deadline = next
	return true
}

// Remaining returns time left until the deadline; never negative.
func (c *Countdown) Remaining() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := c.deadline.Sub(c.now())
	if r < 0 {
		return 0
	}
	return r
}

// Expired reports whether the deadline has passed.
func (c *Countdown) Expired() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now().After(c.deadline)
}
