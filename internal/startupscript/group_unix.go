//go:build !windows

package startupscript

import (
	"os/exec"
	"syscall"
)

// applyProcessGroup puts the shell into its own process group so a
// later kill(-pgid) reaches every descendant — `ngrok &`, sub-shells,
// background daemons. Without Setpgid the shell inherits our group;
// kill -pgid would then take down wick itself.
func applyProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup sends SIGKILL to the negative pid — POSIX convention
// for "every process in this group". Falls back to single-PID kill if
// the group lookup fails (shell already exited, race window).
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	pgid, err := syscall.Getpgid(cmd.Process.Pid)
	if err != nil {
		return cmd.Process.Kill()
	}
	return syscall.Kill(-pgid, syscall.SIGKILL)
}

// releaseProcessGroup is a no-op on Unix — process groups disappear
// when their members exit. The Windows side holds a Job Object handle
// that needs explicit Close, hence the shared interface.
func releaseProcessGroup(cmd *exec.Cmd) {}

// assignToJob is a no-op on Unix — the process group is already
// established by Setpgid at fork. Windows needs a post-Start step to
// attach the process to the Job Object handle created earlier.
func assignToJob(cmd *exec.Cmd) {}
