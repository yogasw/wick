package daemon

import (
	"os"
	"path/filepath"
)

// supervised.go centralises "who is supervising this process" so the
// daemon CLI (start/stop/status) routes to one jalur instead of two
// disconnected ones (PID-file vs systemd).
//
// The problem it solves: `service install` registers a systemd-user
// unit that systemd then owns — it spawns `<app> all`, tracks it by
// cgroup, and respawns on failure. The PID-file-based start/stop knows
// nothing about that, so `status` lied ("not running" while systemd ran
// it) and `stop` fought systemd's respawn. Now every command first asks
// "is a systemd unit in charge?" and delegates when so.

// Source labels the spawn origin of a running daemon, recorded in the
// run.source file next to run.pid so `status` can report it without
// guessing.
type Source string

const (
	SourceCLI     Source = "cli"     // direct `<app> all` / `<app> start` from a shell
	SourceSystemd Source = "systemd" // spawned by the systemd-user unit
	SourceTray    Source = "tray"    // GUI tray started the server in-process
	SourceUnknown Source = "unknown"
)

// sourcePath is the sibling of run.pid holding the spawn Source string.
func sourcePath(p Paths) string {
	return filepath.Join(p.Dir, "run.source")
}

// DetectSource inspects the current process environment to decide how it
// was spawned. systemd injects INVOCATION_ID into every unit it starts,
// which is the most reliable signal that systemd owns this process. The
// caller (`<app> all`) records the result so later `status`/`stop` calls
// know the supervisor without re-deriving it from a different process.
func DetectSource() Source {
	// systemd sets INVOCATION_ID for every unit it launches (PID 1 and
	// user manager alike). Presence ⇒ this process is a systemd unit.
	if os.Getenv("INVOCATION_ID") != "" {
		return SourceSystemd
	}
	// WICK_SPAWN_SOURCE lets the tray / daemon parent stamp the origin
	// explicitly when no supervisor env exists (e.g. tray boots `all` in
	// a goroutine, or `start` detaches a child). Empty ⇒ direct CLI launch.
	if v := os.Getenv("WICK_SPAWN_SOURCE"); v != "" {
		switch Source(v) {
		case SourceTray, SourceCLI, SourceSystemd:
			return Source(v)
		}
	}
	return SourceCLI
}

// WriteSource records the spawn Source next to run.pid. Best-effort —
// a failure here only degrades `status` reporting, never blocks boot.
func WriteSource(p Paths, src Source) {
	_ = os.WriteFile(sourcePath(p), []byte(string(src)+"\n"), 0o644)
}

// ReadSource returns the recorded spawn Source, or SourceUnknown when
// the file is missing / unreadable.
func ReadSource(p Paths) Source {
	data, err := os.ReadFile(sourcePath(p))
	if err != nil {
		return SourceUnknown
	}
	switch s := Source(trimNL(string(data))); s {
	case SourceCLI, SourceSystemd, SourceTray:
		return s
	default:
		return SourceUnknown
	}
}

// SelfRegister writes the running process's own PID + source to the
// daemon files. Called from `<app> all` on boot so the PID file is
// accurate no matter who spawned the process — systemd, the daemon
// parent, or a direct foreground run. Idempotent across restarts.
func SelfRegister(p Paths, src Source) error {
	WriteSource(p, src)
	return writePID(p.PIDFile, os.Getpid())
}

func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}
