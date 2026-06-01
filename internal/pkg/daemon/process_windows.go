//go:build windows

package daemon

import (
	"os"
	"syscall"
)

// processAlive returns true if pid refers to a running process. On
// Windows, os.FindProcess actually verifies process existence (unlike
// Unix), so a successful FindProcess + GetExitCodeProcess STILL_ACTIVE
// is the canonical check.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	const stillActive = 259 // STILL_ACTIVE — process has not terminated
	h, err := syscall.OpenProcess(syscall.PROCESS_QUERY_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer syscall.CloseHandle(h)
	var exitCode uint32
	if err := syscall.GetExitCodeProcess(h, &exitCode); err != nil {
		return false
	}
	return exitCode == stillActive
}

// signalProcess on Windows uses Process.Kill for SIGTERM/SIGKILL —
// the signal package only supports os.Kill semantics there. A
// graceful console-event approach (CTRL_BREAK_EVENT) would require
// allocating a console; not worth the complexity for the daemon's
// limited stop path.
func signalProcess(pid int, sig syscall.Signal) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}
