// Package daemon implements background-process lifecycle for wick:
// Start/Stop/Restart/Status with a PID file. Targets headless
// environments where the system tray is not usable (Termux, SSH
// sessions, Linux servers).
//
// The daemon re-execs the same binary with the existing `all`
// subcommand (server + worker in one process), detached from the
// caller's terminal so the parent shell can exit without killing it.
// Output goes to a log file alongside the PID file so users can tail
// it after start.
package daemon

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/yogasw/wick/internal/userconfig"
)

// Paths bundles the per-app filesystem locations used by the daemon.
type Paths struct {
	Dir      string // ~/.<appname>/
	PIDFile  string // run.pid
	LogFile  string // daemon.log
	ExePath  string // resolved path to the currently running binary
}

// ResolvePaths returns the canonical daemon paths for an app. The
// directory is created if missing.
func ResolvePaths(appName string) (Paths, error) {
	dir, err := userconfig.Dir(appName)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve user dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Paths{}, fmt.Errorf("mkdir %s: %w", dir, err)
	}
	exe, err := os.Executable()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve executable: %w", err)
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return Paths{
		Dir:     dir,
		PIDFile: filepath.Join(dir, "run.pid"),
		LogFile: filepath.Join(dir, "daemon.log"),
		ExePath: exe,
	}, nil
}

// Status reflects the running state of the daemon.
type Status struct {
	Running bool
	PID     int
	Started time.Time // mtime of PID file as a best-effort start time
	LogFile string
	PIDFile string
}

// Check inspects the PID file and returns the current daemon state.
// Stale PID files (process no longer alive) are reported as not
// running; callers may then proceed with Start without manually
// cleaning up.
func Check(p Paths) (Status, error) {
	s := Status{PIDFile: p.PIDFile, LogFile: p.LogFile}
	pid, mtime, err := readPID(p.PIDFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return s, nil
		}
		return s, err
	}
	s.PID = pid
	s.Started = mtime
	s.Running = processAlive(pid)
	return s, nil
}

// Start spawns the binary with the supplied subcommand detached
// from the caller's terminal, redirecting stdout+stderr to the
// daemon log file. Returns ErrAlreadyRunning if a live daemon is
// already recorded in the PID file.
//
// args is the full argv tail passed to the spawned binary — callers
// choose between "all" (headless server + worker, default daemon
// mode) and "tray" (interactive tray icon on GUI hosts).
func Start(p Paths, args []string) (int, error) {
	st, err := Check(p)
	if err != nil {
		return 0, err
	}
	if st.Running {
		return st.PID, ErrAlreadyRunning
	}
	// Stale PID file — remove before claiming a new slot.
	if st.PID != 0 {
		_ = os.Remove(p.PIDFile)
	}
	logF, err := os.OpenFile(p.LogFile, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return 0, fmt.Errorf("open log %s: %w", p.LogFile, err)
	}
	cmd := exec.Command(p.ExePath, args...)
	cmd.Stdout = logF
	cmd.Stderr = logF
	cmd.Stdin = nil
	cmd.SysProcAttr = detachAttr()
	// Set a stable working directory so the daemon doesn't crash if
	// the caller's cwd is later removed.
	if home, err := os.UserHomeDir(); err == nil {
		cmd.Dir = home
	}
	if err := cmd.Start(); err != nil {
		_ = logF.Close()
		return 0, fmt.Errorf("spawn daemon: %w", err)
	}
	pid := cmd.Process.Pid
	// Release parent ref so the child outlives this process cleanly.
	_ = cmd.Process.Release()
	_ = logF.Close()
	if err := writePID(p.PIDFile, pid); err != nil {
		return pid, fmt.Errorf("write pid: %w", err)
	}
	return pid, nil
}

// Stop sends SIGTERM (or the OS equivalent) to the running daemon
// and waits up to timeout for it to exit. Falls back to a force
// kill if the process is still alive at the deadline. ErrNotRunning
// is returned if no live daemon was recorded.
func Stop(p Paths, timeout time.Duration) error {
	st, err := Check(p)
	if err != nil {
		return err
	}
	if !st.Running {
		// Clean up any stale PID file silently.
		_ = os.Remove(p.PIDFile)
		return ErrNotRunning
	}
	if err := signalProcess(st.PID, syscall.SIGTERM); err != nil {
		return fmt.Errorf("send SIGTERM to %d: %w", st.PID, err)
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(st.PID) {
			_ = os.Remove(p.PIDFile)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Hard kill as last resort.
	_ = signalProcess(st.PID, syscall.SIGKILL)
	_ = os.Remove(p.PIDFile)
	return nil
}

// Restart stops the running daemon (if any) then starts a fresh
// instance with the supplied argv tail.
func Restart(p Paths, timeout time.Duration, args []string) (int, error) {
	if err := Stop(p, timeout); err != nil && !errors.Is(err, ErrNotRunning) {
		return 0, err
	}
	return Start(p, args)
}

// TailLog copies the last n bytes of the daemon log to w. Convenience
// for `status` output. Returns nil if the log doesn't exist yet.
func TailLog(p Paths, n int64, w io.Writer) error {
	f, err := os.Open(p.LogFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() > n {
		if _, err := f.Seek(info.Size()-n, io.SeekStart); err != nil {
			return err
		}
	}
	_, err = io.Copy(w, f)
	return err
}

// ErrAlreadyRunning signals Start was called while a live daemon
// was already recorded in the PID file.
var ErrAlreadyRunning = errors.New("daemon already running")

// ErrNotRunning signals Stop was called with no live daemon recorded.
var ErrNotRunning = errors.New("daemon not running")

// readPID parses the PID file and returns the stored pid + mtime.
// Whitespace + trailing newline are tolerated.
func readPID(path string) (int, time.Time, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, time.Time{}, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("parse pid: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return pid, time.Time{}, err
	}
	return pid, info.ModTime(), nil
}

// writePID writes the daemon's pid to the file atomically (write to
// tmp + rename) so a crash mid-write can't leave a partial number.
func writePID(path string, pid int) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
