//go:build !windows

package omp

import "syscall"

// signalGroup signals the RPC process's process group (procgroup.Apply
// made it the leader), so the MCP servers and shells it spawned go too.
func signalGroup(pid int, force bool) {
	if pid <= 0 {
		return
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-pid, sig)
}
