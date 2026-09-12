package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/pkg/upgrade"
	"github.com/yogasw/wick/internal/processctl"
	"github.com/yogasw/wick/pkg/safeexec"
)

// RunningTarget resolves the file a graceful reload will actually execute,
// plus the pid of the daemon that will do the handing over.
//
// It is deliberately argv[0] and not /proc/<pid>/exe. tableflip spawns the
// successor with os.Args[0] resolved through exec.LookPath, so THAT is the
// path whose bytes decide which binary comes up next — the inode currently
// executing is already gone the moment someone replaces the file. Installing
// anywhere else produces the most confusing possible outcome: a reload that
// reports success and brings back the old version.
func RunningTarget(p Paths, appName string) (string, int, error) {
	pid := ServiceMainPID(appName)
	if pid <= 0 {
		filePID, _, err := readPID(p.PIDFile)
		if err != nil {
			return "", 0, ErrNotRunning
		}
		pid = filePID
	}
	if pid <= 0 || !processAlive(pid) {
		return "", 0, ErrNotRunning
	}

	if argv0 := procCmdline0(pid); argv0 != "" {
		switch {
		case filepath.IsAbs(argv0):
			return argv0, pid, nil
		case strings.ContainsRune(argv0, filepath.Separator):
			if cwd := procCwd(pid); cwd != "" {
				return filepath.Join(cwd, argv0), pid, nil
			}
		default:
			if resolved, err := safeexec.LookPath(argv0); err == nil {
				return resolved, pid, nil
			}
		}
	}

	if exe := strings.TrimSuffix(processctl.QueryProcess(pid).Exe, " (deleted)"); exe != "" {
		return exe, pid, nil
	}
	if p.ExePath != "" {
		return p.ExePath, pid, nil
	}
	return "", pid, ErrNotRunning
}

// ProcessImageIs reports whether the process is running the file at path.
// Compared by inode, not by name, so a swapped-but-not-reloaded daemon is
// correctly reported as still running the old image.
func ProcessImageIs(pid int, path string) bool {
	exe := strings.TrimSuffix(processctl.QueryProcess(pid).Exe, " (deleted)")
	if exe == "" {
		return false
	}
	a, err := os.Stat(exe)
	if err != nil {
		return false
	}
	b, err := os.Stat(path)
	if err != nil {
		return false
	}
	return os.SameFile(a, b)
}

// WaitSuccessor blocks until the daemon's main pid moves away from oldPID,
// i.e. the successor has taken over as the service's main process.
func WaitSuccessor(p Paths, appName string, oldPID int, timeout time.Duration) (int, error) {
	started := time.Now()
	deadline := started.Add(timeout)
	for {
		pid := ServiceMainPID(appName)
		if pid <= 0 {
			pid, _, _ = readPID(p.PIDFile)
		}
		if pid > 0 && pid != oldPID && processAlive(pid) {
			return pid, nil
		}
		// The daemon may have REFUSED to start a successor — most often
		// because a previous generation is still draining. It knows that
		// instantly; without reading its answer we would sit here for the
		// whole timeout waiting for a process nobody is starting, which is
		// five minutes of somebody's evening for a one-line reason.
		if why, ok := refusedSince(p.Dir, started); ok {
			return 0, fmt.Errorf("the daemon did not start a successor: %s", why)
		}
		if time.Now().After(deadline) {
			return 0, ErrReloadTimeout
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// RefusedRecently reports a refusal recorded within the last window. Used by
// a reload that does NOT wait for the successor: the daemon answers "I will
// not start one" immediately, and that is worth catching even when nothing is
// being waited for. Anything older belongs to an earlier reload.
func RefusedRecently(dir string, window time.Duration) (string, bool) {
	return refusedSince(dir, time.Now().Add(-window))
}

// refusedSince reports a failed handover recorded AFTER since — an older
// record belongs to a previous reload and must not abort this one.
func refusedSince(dir string, since time.Time) (string, bool) {
	if dir == "" {
		return "", false
	}
	b, err := os.ReadFile(upgrade.HandoverStatePath(dir))
	if err != nil {
		return "", false
	}
	var r struct {
		At    time.Time `json:"at"`
		OK    bool      `json:"ok"`
		Error string    `json:"error"`
	}
	if json.Unmarshal(b, &r) != nil || r.OK || r.Error == "" {
		return "", false
	}
	// A second of slack: the record is written by another process, and two
	// clocks reading the same instant do not have to agree to the nanosecond.
	if r.At.Before(since.Add(-time.Second)) {
		return "", false
	}
	return r.Error, true
}

// WaitDrain blocks until the old process has finished its in-flight work and
// exited. That exit is the moment cron, channels and the schedule runner
// move to the successor, so a deploy script that cares about "fully over"
// waits for this and not merely for the successor to serve HTTP.
func WaitDrain(oldPID int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for processAlive(oldPID) {
		if time.Now().After(deadline) {
			return ErrDrainTimeout
		}
		time.Sleep(time.Second)
	}
	return nil
}

// ErrReloadTimeout signals the successor never became the service's main
// process within the wait window. The old process keeps serving in that
// case — tableflip only steps aside once the successor reports ready — so
// this is a failed upgrade, not an outage.
var ErrReloadTimeout = errors.New("successor did not take over in time")

// ErrDrainTimeout signals the previous process was still finishing work
// when the wait window expired. Harmless on its own: it drains on its own
// schedule, bounded by WICK_DRAIN_TIMEOUT.
var ErrDrainTimeout = errors.New("previous process still draining")
