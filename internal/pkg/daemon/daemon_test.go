package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestResolvePaths_CreatesDir checks the canonical layout under the
// user dir is materialised on first call.
func TestResolvePaths_CreatesDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())

	app := "wick-daemon-test"
	p, err := ResolvePaths(app)
	if err != nil {
		t.Fatalf("ResolvePaths: %v", err)
	}
	if p.Dir == "" || p.PIDFile == "" || p.LogFile == "" || p.ExePath == "" {
		t.Fatalf("incomplete Paths: %+v", p)
	}
	if _, err := os.Stat(p.Dir); err != nil {
		t.Fatalf("expected dir to exist: %v", err)
	}
	if filepath.Base(p.PIDFile) != "run.pid" {
		t.Errorf("unexpected pidfile name: %s", p.PIDFile)
	}
	if filepath.Base(p.LogFile) != "daemon.log" {
		t.Errorf("unexpected logfile name: %s", p.LogFile)
	}
}

// TestWritePIDReadPID_RoundTrip validates that writePID + readPID
// agree on the integer and that mtime is reported.
func TestWritePIDReadPID_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	pf := filepath.Join(dir, "run.pid")
	if err := writePID(pf, 12345); err != nil {
		t.Fatalf("writePID: %v", err)
	}
	pid, mtime, err := readPID(pf)
	if err != nil {
		t.Fatalf("readPID: %v", err)
	}
	if pid != 12345 {
		t.Errorf("pid round-trip mismatch: got %d", pid)
	}
	if time.Since(mtime) > 5*time.Second {
		t.Errorf("mtime too old: %v", mtime)
	}
}

// TestReadPID_MissingFile returns os.ErrNotExist so Check can detect
// the "never started" case without surfacing it as a hard error.
func TestReadPID_MissingFile(t *testing.T) {
	dir := t.TempDir()
	pf := filepath.Join(dir, "run.pid")
	_, _, err := readPID(pf)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected os.ErrNotExist, got %v", err)
	}
}

// TestProcessAlive_Self confirms the current process is detected as
// alive — sanity check for the platform-specific implementation.
func TestProcessAlive_Self(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("expected self to be alive")
	}
}

// TestProcessAlive_Bogus confirms an obviously-invalid PID reads as
// dead. Skips zero on Windows where OpenProcess(0) returns a pseudo
// handle to the System process.
func TestProcessAlive_Bogus(t *testing.T) {
	if processAlive(-1) {
		t.Errorf("expected -1 to read as dead")
	}
	if processAlive(0) {
		t.Errorf("expected 0 to read as dead")
	}
	// 99999999 is well beyond any realistic active PID.
	if processAlive(99999999) {
		t.Errorf("expected huge PID to read as dead")
	}
}

// TestCheck_NoFile reports "not running" with zero PID when nothing
// has ever been started.
func TestCheck_NoFile(t *testing.T) {
	tmp := t.TempDir()
	p := Paths{
		Dir:     tmp,
		PIDFile: filepath.Join(tmp, "run.pid"),
		LogFile: filepath.Join(tmp, "daemon.log"),
		ExePath: "ignored",
	}
	st, err := Check(p)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if st.Running || st.PID != 0 {
		t.Errorf("expected empty status, got %+v", st)
	}
}

// TestCheck_StalePID reports "not running" but exposes the stale PID
// so callers can log / report on the previous instance.
func TestCheck_StalePID(t *testing.T) {
	tmp := t.TempDir()
	p := Paths{
		Dir:     tmp,
		PIDFile: filepath.Join(tmp, "run.pid"),
		LogFile: filepath.Join(tmp, "daemon.log"),
		ExePath: "ignored",
	}
	if err := writePID(p.PIDFile, 99999999); err != nil {
		t.Fatalf("seed stale pid: %v", err)
	}
	st, err := Check(p)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if st.Running {
		t.Error("stale PID should not be marked running")
	}
	if st.PID != 99999999 {
		t.Errorf("stale PID should be surfaced: got %d", st.PID)
	}
}

// TestStop_NotRunning returns ErrNotRunning and quietly removes a
// stale PID file so the next Start has a clean slot.
func TestStop_NotRunning(t *testing.T) {
	tmp := t.TempDir()
	p := Paths{
		Dir:     tmp,
		PIDFile: filepath.Join(tmp, "run.pid"),
		LogFile: filepath.Join(tmp, "daemon.log"),
		ExePath: "ignored",
	}
	// No PID file → ErrNotRunning straight away.
	if err := Stop(p, time.Second); !errors.Is(err, ErrNotRunning) {
		t.Errorf("expected ErrNotRunning, got %v", err)
	}
	// Stale PID → still ErrNotRunning, file gone afterwards.
	if err := writePID(p.PIDFile, 99999999); err != nil {
		t.Fatalf("seed stale pid: %v", err)
	}
	if err := Stop(p, time.Second); !errors.Is(err, ErrNotRunning) {
		t.Errorf("expected ErrNotRunning on stale, got %v", err)
	}
	if _, err := os.Stat(p.PIDFile); !os.IsNotExist(err) {
		t.Error("stale PID file should be removed")
	}
}

// TestDetachAttr_NonNil sanity-checks the platform-specific detach
// attrs are populated. Field-level assertions live in the platform
// build-tag files (Setsid on unix, HideWindow on windows) — both
// compile against the matching syscall.SysProcAttr shape.
func TestDetachAttr_NonNil(t *testing.T) {
	if a := detachAttr(); a == nil {
		t.Fatal("detachAttr returned nil")
	}
}
