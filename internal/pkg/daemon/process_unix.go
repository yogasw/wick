//go:build !windows

package daemon

import (
	"os"
	"syscall"
)

// processAlive returns true if pid refers to a running process.
// Sends signal 0 — kernel-level liveness check that doesn't actually
// deliver a signal. EPERM (no permission to signal but process exists)
// also counts as alive.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	return os.IsPermission(err)
}

// signalProcess sends sig to pid. Wraps Process.Signal for symmetry
// with the Windows path which uses a different mechanism.
func signalProcess(pid int, sig syscall.Signal) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(sig)
}
