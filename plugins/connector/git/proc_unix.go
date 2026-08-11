//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// setProcAttr puts git in its own process group so a timeout can kill the whole
// tree. git spawns helpers (git-remote-https, credential helpers); killing only
// the parent orphans them.
//
// It adds to SysProcAttr rather than replacing it, so it composes with anything
// the spawn wrapper may already have set there.
func setProcAttr(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killGroup kills the process group created by setProcAttr.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
