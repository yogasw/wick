//go:build windows

package daemon

import (
	"errors"
	"os"
	"syscall"

	"github.com/yogasw/wick/internal/processctl"
)

// processAlive + queryProcess re-export processctl's OS-process
// primitives so daemon.Check / Stop keep their call sites while the
// liveness + identity logic lives in one shared place.
func processAlive(pid int) bool                   { return processctl.ProcessAlive(pid) }
func queryProcess(pid int) processctl.ProcessInfo { return processctl.QueryProcess(pid) }

// signalProcess on Windows kills the process (no POSIX signals).
// Daemon-specific; Stop calls this in place of SIGTERM/SIGKILL.
func signalProcess(pid int, sig syscall.Signal) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

// reloadSignal is unavailable on Windows: there are no POSIX signals, and
// descriptor passing (which a graceful handoff needs) does not exist either.
// Callers fall back to restart.
func reloadSignal(int) error {
	return errors.New("graceful reload is not supported on Windows — use restart")
}
