package memscope

import (
	"os"
	"path/filepath"
	"testing"
)

func writeV1Scope(t *testing.T, root, slice, unit, maxUsage string) {
	t.Helper()
	dir := filepath.Join(root, slice, unit+".scope")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if maxUsage != "" {
		if err := os.WriteFile(filepath.Join(dir, "memory.max_usage_in_bytes"), []byte(maxUsage), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The one thing v1 can report with confidence: how much memory a scope
// used at its worst point.
func TestReadStatsV1At_ReportsPeak(t *testing.T) {
	root := t.TempDir()
	writeV1Scope(t, root, SliceName, "claude-agent-1", "536870912\n")

	got := ReadStatsV1At(root, SliceName, "claude-agent-1")
	if !got.Known {
		t.Fatal("stats reported unknown despite a readable scope")
	}
	if got.PeakBytes != 536870912 {
		t.Fatalf("PeakBytes = %d, want 536870912", got.PeakBytes)
	}
}

// The honest limitation this whole file documents: v1 has no per-group
// kill counter, so OOMKills must never be anything but 0 from this
// reader — a fabricated nonzero value would be a guess dressed as
// evidence, exactly what ClassifyExit's "never guess" rule forbids.
func TestReadStatsV1At_NeverReportsAKill(t *testing.T) {
	root := t.TempDir()
	writeV1Scope(t, root, SliceName, "claude-agent-2", "999999999\n")

	got := ReadStatsV1At(root, SliceName, "claude-agent-2")
	if got.OOMKills != 0 {
		t.Fatalf("OOMKills = %d, want 0 — v1 has no kill counter to read", got.OOMKills)
	}
}

// A scope that was never created (or was cleaned up already) must read
// as unknown, not as "zero peak" — the same "no evidence, no verdict"
// contract ReadStatsAt holds for the v2/systemd path.
func TestReadStatsV1At_MissingScopeIsUnknown(t *testing.T) {
	got := ReadStatsV1At(t.TempDir(), SliceName, "claude-agent-gone")
	if got.Known {
		t.Fatal("a missing v1 scope reported Known=true")
	}
}

// A malformed counter must not panic or invent a number.
func TestReadStatsV1At_MalformedIsSafe(t *testing.T) {
	root := t.TempDir()
	writeV1Scope(t, root, SliceName, "claude-agent-3", "not-a-number\n")

	got := ReadStatsV1At(root, SliceName, "claude-agent-3")
	if got.PeakBytes != 0 {
		t.Fatalf("malformed peak produced %d bytes", got.PeakBytes)
	}
}
