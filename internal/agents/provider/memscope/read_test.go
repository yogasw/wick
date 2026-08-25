package memscope

import (
	"os"
	"path/filepath"
	"testing"
)

func writeScope(t *testing.T, root, unit, events, peak string) {
	t.Helper()
	dir := filepath.Join(root, SliceName, unit+".scope")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if events != "" {
		if err := os.WriteFile(filepath.Join(dir, "memory.events"), []byte(events), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if peak != "" {
		if err := os.WriteFile(filepath.Join(dir, "memory.peak"), []byte(peak), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// The whole point of the readback: an OOM kill is indistinguishable from
// any other SIGKILL by exit code alone, so oom_kill is the only evidence.
func TestReadStatsAt_DetectsOOMKill(t *testing.T) {
	root := t.TempDir()
	writeScope(t, root, "claude-agent-1",
		"low 0\nhigh 0\nmax 12\noom 3\noom_kill 1\n", "1610612736\n")

	got := ReadStatsAt(root, "claude-agent-1")
	if !got.Known {
		t.Fatal("stats reported unknown despite a readable scope")
	}
	if got.OOMKills != 1 {
		t.Fatalf("OOMKills = %d, want 1", got.OOMKills)
	}
	if got.PeakBytes != 1610612736 {
		t.Fatalf("PeakBytes = %d, want 1610612736", got.PeakBytes)
	}
}

// A scope that hit its ceiling but was never killed is not an OOM.
func TestReadStatsAt_MaxWithoutKillIsNotOOM(t *testing.T) {
	root := t.TempDir()
	writeScope(t, root, "claude-agent-2", "max 5\noom_kill 0\n", "500\n")

	got := ReadStatsAt(root, "claude-agent-2")
	if !got.Known {
		t.Fatal("readable scope reported unknown")
	}
	if got.OOMKills != 0 {
		t.Fatalf("OOMKills = %d, want 0", got.OOMKills)
	}
}

// --collect reaps a scope as soon as its last process exits, so the read
// races the reap. A missing scope must read as "unknown", never as a
// confident "not OOM" and never as a false OOM.
func TestReadStatsAt_MissingScopeIsUnknown(t *testing.T) {
	got := ReadStatsAt(t.TempDir(), "claude-agent-gone")
	if got.Known {
		t.Fatal("a missing scope reported Known=true")
	}
	if got.OOMKills != 0 {
		t.Fatalf("a missing scope reported %d kills", got.OOMKills)
	}
}

// Kernel files gain fields across versions; an unparseable line must not
// panic or invent a kill.
func TestReadStatsAt_MalformedIsSafe(t *testing.T) {
	root := t.TempDir()
	writeScope(t, root, "claude-agent-3", "garbage\noom_kill\noom_kill xyz\n", "not-a-number\n")

	got := ReadStatsAt(root, "claude-agent-3")
	if got.OOMKills != 0 {
		t.Fatalf("malformed events produced %d kills", got.OOMKills)
	}
	if got.PeakBytes != 0 {
		t.Fatalf("malformed peak produced %d bytes", got.PeakBytes)
	}
}

// A scope with events but no peak file is still a usable verdict: the
// kill is what matters, the peak is detail.
func TestReadStatsAt_MissingPeakStillReportsKill(t *testing.T) {
	root := t.TempDir()
	writeScope(t, root, "claude-agent-4", "oom_kill 2\n", "")

	got := ReadStatsAt(root, "claude-agent-4")
	if !got.Known || got.OOMKills != 2 {
		t.Fatalf("stats = %+v, want Known with 2 kills", got)
	}
	if got.PeakBytes != 0 {
		t.Fatalf("PeakBytes = %d, want 0 when unreadable", got.PeakBytes)
	}
}
