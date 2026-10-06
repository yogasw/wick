package agents

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
)

func TestProviderIdleCompactorOncePerIdleStretch(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	last := now.Add(-time.Hour)
	used := 90_000
	busy := false
	var compacted []string
	c := &providerIdleCompactor{
		Sessions: func() []idleCandidate {
			return []idleCandidate{{SessionID: "s1", LastActive: last, Busy: busy}}
		},
		Usage: func(string) (string, int, int, bool) { return "claude", used, 200_000, true },
		Policy: func(key string) provider.IdleCompactPolicy {
			if key != "claude" {
				t.Fatalf("policy asked for %q", key)
			}
			return provider.IdleCompactPolicy{Enabled: true, Idle: 30 * time.Minute, Trigger: provider.IdleCompactPercent, Threshold: 40}
		},
		Compact: func(_ context.Context, id string) error { compacted = append(compacted, id); return nil },
		Now:     func() time.Time { return now },
	}
	if got := c.Tick(context.Background()); len(got) != 1 {
		t.Fatalf("first tick compacted %v, want s1", got)
	}
	// The compact turn bumps LastActive: settle, do not compact again.
	last = now.Add(-time.Minute)
	now = now.Add(time.Hour)
	c.Tick(context.Background())
	c.Tick(context.Background())
	if len(compacted) != 1 {
		t.Fatalf("compacted %d times, want 1", len(compacted))
	}
	// The user comes back, then goes idle again: armed again.
	last = now.Add(-40 * time.Minute)
	c.Tick(context.Background())
	if len(compacted) != 2 {
		t.Fatalf("after new activity compacted %d times, want 2", len(compacted))
	}
}

func TestProviderIdleCompactorSkips(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	pol := provider.IdleCompactPolicy{Enabled: true, Idle: 30 * time.Minute, Trigger: provider.IdleCompactPercent, Threshold: 40}
	cases := []struct {
		name string
		c    idleCandidate
		used int
		pol  provider.IdleCompactPolicy
	}{
		{"busy", idleCandidate{SessionID: "s", LastActive: now.Add(-time.Hour), Busy: true}, 150_000, pol},
		{"too recent", idleCandidate{SessionID: "s", LastActive: now.Add(-10 * time.Minute)}, 150_000, pol},
		{"older than lookback", idleCandidate{SessionID: "s", LastActive: now.Add(-48 * time.Hour)}, 150_000, pol},
		{"below threshold", idleCandidate{SessionID: "s", LastActive: now.Add(-time.Hour)}, 10_000, pol},
		{"disabled", idleCandidate{SessionID: "s", LastActive: now.Add(-time.Hour)}, 150_000, provider.IdleCompactPolicy{}},
	}
	for _, tc := range cases {
		n := 0
		c := &providerIdleCompactor{
			Sessions: func() []idleCandidate { return []idleCandidate{tc.c} },
			Usage:    func(string) (string, int, int, bool) { return "claude", tc.used, 200_000, true },
			Policy:   func(string) provider.IdleCompactPolicy { return tc.pol },
			Compact:  func(context.Context, string) error { n++; return nil },
			Now:      func() time.Time { return now },
		}
		c.Tick(context.Background())
		if n != 0 {
			t.Errorf("%s: compacted", tc.name)
		}
	}
}

func TestProviderIdleCompactorCapsPerTick(t *testing.T) {
	now := time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC)
	var rows []idleCandidate
	for _, id := range []string{"a", "b", "c", "d"} {
		rows = append(rows, idleCandidate{SessionID: id, LastActive: now.Add(-time.Hour)})
	}
	n := 0
	c := &providerIdleCompactor{
		Sessions: func() []idleCandidate { return rows },
		Usage:    func(string) (string, int, int, bool) { return "claude", 150_000, 200_000, true },
		Policy: func(string) provider.IdleCompactPolicy {
			return provider.IdleCompactPolicy{Enabled: true, Idle: time.Minute, Trigger: provider.IdleCompactPercent, Threshold: 40}
		},
		Compact: func(context.Context, string) error { n++; return nil },
		Now:     func() time.Time { return now },
	}
	c.Tick(context.Background())
	if n != providerIdleCompactPerTick {
		t.Fatalf("first tick compacted %d, want %d", n, providerIdleCompactPerTick)
	}
	c.Tick(context.Background())
	if n != 4 {
		t.Fatalf("after two ticks compacted %d, want 4", n)
	}
}
