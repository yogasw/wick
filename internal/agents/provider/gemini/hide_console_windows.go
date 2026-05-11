//go:build windows

package gemini

import (
	"os/exec"
	"syscall"
)

func hideConsole(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
	}
}
