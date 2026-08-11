//go:build windows

package main

import (
	"os/exec"
	"strconv"
	"syscall"

	"github.com/yogasw/wick/pkg/safeexec"
)

// setProcAttr hides the console window of the spawned git process. Windows has no
// POSIX process group to set at spawn time; killGroup uses taskkill /T instead.
//
// It adds to SysProcAttr rather than replacing it: safeexec sets CmdLine there
// when the target is a .bat/.cmd shim, and overwriting the struct would silently
// drop that quoting fix.
func setProcAttr(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
}

// killGroup kills git and every child it spawned. taskkill /T walks the process
// tree, which is the Windows equivalent of killing a process group. The plain
// Process.Kill afterwards is the fallback for the case where taskkill is not on
// PATH — without it a stuck git would never be reaped and Wait would block.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	kill := safeexec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
	if kill.SysProcAttr == nil {
		kill.SysProcAttr = &syscall.SysProcAttr{}
	}
	kill.SysProcAttr.HideWindow = true
	if err := kill.Run(); err != nil {
		_ = cmd.Process.Kill()
	}
}
