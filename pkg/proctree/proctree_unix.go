//go:build !windows

package proctree

import (
	"os/exec"
	"syscall"
)

// apply puts the child into its own process group so a later
// kill(-pgid) reaches every descendant — sub-shells, `cmd &` daemons,
// forked helpers. Without Setpgid the child inherits our group and
// kill(-pgid) would take down the parent (wick) itself.
func apply(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// assign is a no-op on Unix — the group is established at fork by
// Setpgid; there is no post-Start attach step.
func assign(*exec.Cmd) {}

// killGroup sends SIGKILL to the negative pid (POSIX "whole group").
// Falls back to a single-PID kill if the group lookup fails (the child
// already exited — race window).
func killGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		return cmd.Process.Kill()
	}
	return syscall.Kill(-pgid, syscall.SIGKILL)
}

// release is a no-op on Unix — process groups disappear when their
// members exit.
func release(*exec.Cmd) {}
