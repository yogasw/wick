package upgrade

import (
	"context"
	"testing"
)

func TestCancelStuckReportsOnlyWhatStopped(t *testing.T) {
	stuckMu.Lock()
	saved := stuckCancels
	stuckCancels = map[string]func(context.Context) int{}
	stuckMu.Unlock()
	t.Cleanup(func() {
		stuckMu.Lock()
		stuckCancels = saved
		stuckMu.Unlock()
	})

	RegisterStuckCancel("busy", func(context.Context) int { return 2 })
	RegisterStuckCancel("idle", func(context.Context) int { return 0 })
	RegisterStuckCancel("", func(context.Context) int { return 9 })
	RegisterStuckCancel("nil", nil)

	got := CancelStuck(context.Background())
	if len(got) != 1 || got["busy"] != 2 {
		t.Fatalf("CancelStuck = %v, want map[busy:2]", got)
	}
}

func TestStuckWorkTimeoutEnv(t *testing.T) {
	t.Setenv("WICK_DRAIN_STUCK_TIMEOUT", "")
	if got := StuckWorkTimeout(); got.Minutes() != 30 {
		t.Fatalf("default = %v, want 30m", got)
	}
	t.Setenv("WICK_DRAIN_STUCK_TIMEOUT", "0")
	if got := StuckWorkTimeout(); got != 0 {
		t.Fatalf("0 = %v, want off", got)
	}
}
