package engine

import (
	"testing"
	"time"
)

// TestRetryJitter_AtLeastBaseBackoff verifies that jitter is never less than
// the base backoff duration.
func TestRetryJitter_AtLeastBaseBackoff(t *testing.T) {
	base := 2 * time.Second
	for i := 0; i < 1000; i++ {
		got := retryJitter(base)
		if got < base {
			t.Fatalf("retryJitter(%v) = %v; want >= %v", base, got, base)
		}
	}
}

// TestRetryJitter_AtMost150Percent verifies that jitter never exceeds 1.5x the
// base backoff duration.
func TestRetryJitter_AtMost150Percent(t *testing.T) {
	base := 2 * time.Second
	max := base + base/2
	for i := 0; i < 1000; i++ {
		got := retryJitter(base)
		if got > max {
			t.Fatalf("retryJitter(%v) = %v; want <= %v (1.5x base)", base, got, max)
		}
	}
}

// TestRetryJitter_Spread verifies that repeated calls produce different values,
// confirming that jitter is not a no-op (i.e. randomness is applied).
func TestRetryJitter_Spread(t *testing.T) {
	base := 4 * time.Second
	seen := map[time.Duration]bool{}
	const samples = 200
	for i := 0; i < samples; i++ {
		seen[retryJitter(base)] = true
	}
	// With a 2-second jitter window at nanosecond resolution, any reasonable
	// RNG should produce well more than 1 distinct value in 200 samples.
	if len(seen) < 2 {
		t.Fatalf("retryJitter produced only %d distinct values in %d samples; expected spread", len(seen), samples)
	}
}

// TestRetryJitter_SmallBackoff verifies correctness when backoff is very small
// (1 ns), where backoff/2+1 == 1, so jitter must be in [1ns, 1ns].
func TestRetryJitter_SmallBackoff(t *testing.T) {
	base := time.Duration(1)
	for i := 0; i < 100; i++ {
		got := retryJitter(base)
		if got < base {
			t.Fatalf("retryJitter(%v) = %v; want >= %v", base, got, base)
		}
		max := base + base/2 + 1
		if got > max {
			t.Fatalf("retryJitter(%v) = %v; want <= %v", base, got, max)
		}
	}
}
