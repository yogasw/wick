//go:build !windows

package daemon

import (
	"os"
	"syscall"

	"github.com/yogasw/wick/internal/processctl"
)

// processAlive + queryProcess are thin re-exports of processctl's
// OS-process primitives so daemon.Check / Stop keep their old call
// sites while the actual liveness + identity logic lives in one place.
func processAlive(pid int) bool                   { return processctl.ProcessAlive(pid) }
func queryProcess(pid int) processctl.ProcessInfo { return processctl.QueryProcess(pid) }

// signalProcess sends sig to pid. Daemon-specific (Stop uses SIGTERM);
// not part of the shared liveness primitives.
func signalProcess(pid int, sig syscall.Signal) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(sig)
}

// reloadSignal asks a running daemon to hand over to a new binary. SIGHUP is
// the conventional "reload" signal, and it is what the upgrade watcher inside
// the daemon listens for.
func reloadSignal(pid int) error { return signalProcess(pid, syscall.SIGHUP) }
