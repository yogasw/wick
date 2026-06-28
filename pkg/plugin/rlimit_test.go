//go:build linux || android

package plugin

import (
	"testing"
)

func TestEnvIntParsing(t *testing.T) {
	t.Setenv("WICK_PLUGIN_RLIMIT_NOFILE", "256")
	if got := envInt("WICK_PLUGIN_RLIMIT_NOFILE"); got != 256 {
		t.Fatalf("envInt = %d, want 256", got)
	}
	t.Setenv("WICK_PLUGIN_RLIMIT_NOFILE", "")
	if got := envInt("WICK_PLUGIN_RLIMIT_NOFILE"); got != 0 {
		t.Fatalf("empty env should be 0, got %d", got)
	}
	t.Setenv("WICK_PLUGIN_RLIMIT_NOFILE", "notanumber")
	if got := envInt("WICK_PLUGIN_RLIMIT_NOFILE"); got != 0 {
		t.Fatalf("garbage env should be 0, got %d", got)
	}
}

func TestApplyRlimitsSoftFails(t *testing.T) {
	// No env set → no-op, must not panic.
	applyRlimits()
	// A safe value of NOFILE — must not panic (lowering within the soft limit).
	t.Setenv("WICK_PLUGIN_RLIMIT_NOFILE", "64")
	applyRlimits()
}
