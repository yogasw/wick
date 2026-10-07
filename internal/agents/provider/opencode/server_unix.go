//go:build !windows

package opencode

import "syscall"

// signalServerGroup signals the server's process group (procgroup.Apply
// made it the leader), so the MCP servers and shells it spawned go too.
func signalServerGroup(pid int, force bool) {
	if pid <= 0 {
		return
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-pid, sig)
}
