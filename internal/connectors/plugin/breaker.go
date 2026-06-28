package plugin

import (
	"fmt"
	"time"
)

const (
	breakerBase = 1 * time.Second
	breakerMax  = 60 * time.Second
)

type breaker struct {
	fails     int
	openUntil time.Time
}

// backoff returns base * 2^(fails-1), capped at breakerMax.
func backoff(fails int) time.Duration {
	if fails <= 1 {
		return breakerBase
	}
	d := breakerBase << (fails - 1)
	if d <= 0 || d > breakerMax {
		return breakerMax
	}
	return d
}

// breakerOpenLocked returns a non-nil error when the circuit for key is open.
// Caller holds m.mu.
func (m *Manager) breakerOpenLocked(key string) error {
	b := m.breakers[key]
	if b != nil && m.now().Before(b.openUntil) {
		return fmt.Errorf("connector %q unavailable: circuit open until %s", key, b.openUntil.Format(time.RFC3339))
	}
	return nil
}

// recordFailureLocked grows the backoff window after a failed spawn.
func (m *Manager) recordFailureLocked(key string) {
	if m.breakers == nil {
		m.breakers = map[string]*breaker{}
	}
	b := m.breakers[key]
	if b == nil {
		b = &breaker{}
		m.breakers[key] = b
	}
	b.fails++
	b.openUntil = m.now().Add(backoff(b.fails))
}

func (m *Manager) resetBreakerLocked(key string) {
	delete(m.breakers, key)
}
