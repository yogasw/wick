package provider

import (
	"testing"
	"time"
)

// writeSpawn lays down one spawn log the way a real spawn would: a
// pre-start record, then a start carrying the pid, then optionally an exit.
func writeSpawn(t *testing.T, s *SpawnLogger, session string, pid int, exitReason string) string {
	t.Helper()
	at := time.Now().UTC()
	path := s.Path("claude", "work", session, at)
	if err := s.Append(path, SpawnEvent{Type: "start", At: at, ProviderType: "claude", ProviderName: "work", SessionID: session}); err != nil {
		t.Fatalf("append start: %v", err)
	}
	if pid != 0 {
		if err := s.Append(path, SpawnEvent{Type: "start", At: at, ProviderType: "claude", ProviderName: "work", SessionID: session, PID: pid}); err != nil {
			t.Fatalf("append start pid: %v", err)
		}
	}
	if exitReason != "" {
		if err := s.Append(path, SpawnEvent{Type: "exit", At: at, SessionID: session, ExitReason: exitReason}); err != nil {
			t.Fatalf("append exit: %v", err)
		}
	}
	return path
}

func reasonOf(t *testing.T, s *SpawnLogger, path string) string {
	t.Helper()
	events, err := s.Read(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	reason := ""
	for _, ev := range events {
		if ev.Type == "exit" {
			reason = ev.ExitReason
		}
	}
	return reason
}

func TestReconcileOrphansClosesDeadSpawns(t *testing.T) {
	s := NewSpawnLogger(t.TempDir())

	deadPID := writeSpawn(t, s, "dead", 4242, "")
	livePID := writeSpawn(t, s, "live", 4243, "")
	alreadyExited := writeSpawn(t, s, "done", 4244, "stopped")
	noPID := writeSpawn(t, s, "nopid", 0, "")

	alive := func(pid int) bool { return pid == 4243 }

	closed, err := s.ReconcileOrphans(alive)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if closed != 2 {
		t.Errorf("closed = %d, want 2 (the dead pid and the one with no pid)", closed)
	}

	if got := reasonOf(t, s, deadPID); got != OrphanExitReason {
		t.Errorf("dead spawn reason = %q, want %q", got, OrphanExitReason)
	}
	if got := reasonOf(t, s, noPID); got != OrphanExitReason {
		t.Errorf("pid-less spawn reason = %q, want %q", got, OrphanExitReason)
	}
	// A running process must keep reading as running — claiming otherwise
	// is the same lie in the other direction.
	if got := reasonOf(t, s, livePID); got != "" {
		t.Errorf("live spawn reason = %q, want empty", got)
	}
	// An exit already on file is never rewritten: the real reason is more
	// informative than "orphaned".
	if got := reasonOf(t, s, alreadyExited); got != "stopped" {
		t.Errorf("finished spawn reason = %q, want stopped", got)
	}
}

func TestReconcileOrphansIsIdempotent(t *testing.T) {
	s := NewSpawnLogger(t.TempDir())
	writeSpawn(t, s, "dead", 4242, "")
	dead := func(int) bool { return false }

	if closed, err := s.ReconcileOrphans(dead); err != nil || closed != 1 {
		t.Fatalf("first pass: closed=%d err=%v, want 1, nil", closed, err)
	}
	// Running at boot AND on every Providers page load means this repeats
	// constantly; a second pass must find nothing left to close rather than
	// appending another exit to the same file.
	if closed, err := s.ReconcileOrphans(dead); err != nil || closed != 0 {
		t.Fatalf("second pass: closed=%d err=%v, want 0, nil", closed, err)
	}
}

func TestReconcileOrphansEmptyDir(t *testing.T) {
	s := NewSpawnLogger(t.TempDir())
	closed, err := s.ReconcileOrphans(func(int) bool { return false })
	if err != nil {
		t.Fatalf("reconcile on empty dir: %v", err)
	}
	if closed != 0 {
		t.Errorf("closed = %d, want 0", closed)
	}
}
