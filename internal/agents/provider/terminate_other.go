//go:build !windows

package provider

import "syscall"

// Process-group teardown (POSIX).
//
// An agent CLI spawns descendants of its own — MCP servers, shells, tool
// subprocesses. Killing only the leader leaves those orphaned: they keep
// running, keep file handles open, and on a long-lived install they
// accumulate. Putting the child in its own process group lets the whole
// tree be signalled at once.
//
// The escalation is SIGTERM → grace → SIGKILL, and it is NOT conditional
// on the leader having exited. A descendant that ignores SIGTERM must
// still be reaped, and gating escalation on the leader's exit is exactly
// the bug the prior art documented fixing.

// The process group itself is established at spawn time by
// provider/procgroup, shared by every spawner. This file only tears one
// down.

// signalGroup sends sig to the process group led by pid.
//
// The negative pid addresses the group rather than the single process —
// that is what reaches descendants the agent spawned.
func signalGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return nil
	}
	return syscall.Kill(-pid, sig)
}

// terminateGroupGracefully asks a process group to exit, then forces it.
// Reports whether the graceful signal was delivered at all.
func terminateGroupGracefully(pid int) bool {
	return signalGroup(pid, syscall.SIGTERM) == nil
}

// killGroup force-kills a process group.
func killGroup(pid int) {
	_ = signalGroup(pid, syscall.SIGKILL)
}
