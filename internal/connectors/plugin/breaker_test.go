package plugin

import (
	"errors"
	"sync"
	"testing"
	"time"
)

func TestBreakerOpensAfterSpawnFailures(t *testing.T) {
	now := time.Unix(0, 0)
	m := &Manager{
		entries:  map[string]*entry{},
		binaries: map[string]string{},
		breakers: map[string]*breaker{},
		now:      func() time.Time { return now },
		stop:     make(chan struct{}),
	}
	m.cond = sync.NewCond(&m.mu)
	m.killFn = func(string) {}
	calls := 0
	m.spawnFn = func(string) (*entry, error) { calls++; return nil, errors.New("boom") }

	if _, err := m.Client("x"); err == nil {
		t.Fatal("first spawn should fail")
	}
	if calls != 1 {
		t.Fatalf("spawn should have run once, got %d", calls)
	}
	if _, err := m.Client("x"); err == nil {
		t.Fatal("circuit should be open")
	}
	if calls != 1 {
		t.Fatalf("spawn must be skipped while open, got %d calls", calls)
	}
	now = time.Unix(2, 0)
	if _, err := m.Client("x"); err == nil {
		t.Fatal("trial spawn still fails")
	}
	if calls != 2 {
		t.Fatalf("trial spawn should run, got %d calls", calls)
	}
}

func TestBackoffCapsAtMax(t *testing.T) {
	if got := backoff(1); got != time.Second {
		t.Fatalf("backoff(1)=%s, want 1s", got)
	}
	if got := backoff(100); got != breakerMax {
		t.Fatalf("backoff(100)=%s, want %s", got, breakerMax)
	}
}
