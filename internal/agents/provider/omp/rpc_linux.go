//go:build linux

package omp

import (
	"os/exec"
	"syscall"
)

// dieWithParent makes the kernel SIGKILL the RPC process when wick dies without
// running its shutdown (crash, SIGKILL), so a server is never orphaned.
func dieWithParent(c *exec.Cmd) {
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Pdeathsig = syscall.SIGKILL
}
