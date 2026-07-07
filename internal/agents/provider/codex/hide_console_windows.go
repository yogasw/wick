//go:build windows

package codex

import (
	"os/exec"
	"syscall"
)

func hideConsole(c *exec.Cmd) {
	// Merge, don't replace: safeexec.Command may already have set
	// SysProcAttr.CmdLine to work around the .cmd/.bat quoting bug.
	// Overwriting the whole struct would drop that and reintroduce the
	// "'C:\Program' is not recognized" failure for space-containing args.
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.HideWindow = true
	c.SysProcAttr.CreationFlags = 0x08000000
}
