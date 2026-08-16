package memreport

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// The sampler must exit when its context is cancelled. Started on a
// context that is never cancelled, it leaks for the life of the process —
// a goroutine leak inside the feature whose job is bounding resource use.
func TestSampler_StopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Sampler{
		History:  NewHistory(time.Hour, 100),
		Interval: 10 * time.Millisecond,
		Names:    []string{"nothing-matches-this"},
	}

	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	// Give it long enough to have ticked at least once.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sampler did not exit after its context was cancelled")
	}
}

// A zero interval must not spin: a tight loop reading /proc would peg a
// core doing nothing useful.
func TestSampler_ZeroIntervalDoesNotSpin(t *testing.T) {
	var samples atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := &Sampler{
		History:  NewHistory(time.Hour, 100),
		Interval: 0, // must fall back to a sane default, not tick continuously
		Names:    []string{"nothing"},
		Enabled: func() bool {
			samples.Add(1)
			return false
		},
	}
	go s.Run(ctx)

	time.Sleep(200 * time.Millisecond)
	cancel()

	// With the 15s fallback, 200ms cannot produce a sample. A spinning
	// loop would produce thousands.
	if n := samples.Load(); n > 2 {
		t.Fatalf("zero interval produced %d ticks in 200ms — it is spinning", n)
	}
}

// Sampling must be skippable at runtime, so turning history off in the UI
// stops the /proc walk without restarting wick.
func TestSampler_RespectsEnabled(t *testing.T) {
	h := NewHistory(time.Hour, 100)
	s := &Sampler{History: h, Names: []string{"anything"}, Enabled: func() bool { return false }}

	s.sample(time.Now())

	if got := h.Stats().MachinePoints; got != 0 {
		t.Fatalf("recorded %d points while disabled, want 0", got)
	}
}

// A nil History must not panic — the sampler can outlive a partially
// constructed server during shutdown.
func TestSampler_NilHistoryIsSafe(t *testing.T) {
	s := &Sampler{Names: []string{"anything"}}
	s.sample(time.Now()) // must not panic
}
