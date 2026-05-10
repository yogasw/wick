//go:build windows

package provider

import (
	"os/exec"
	"syscall"
)

// hideConsole prevents the spawned subprocess console window from
// flashing on screen when wick runs from the system tray. Same pattern
// as internal/systemtray/{editor,notify}_windows.go and the claude
// spawner: HideWindow + CREATE_NO_WINDOW (0x08000000).
//
// Used by Probe (--version) so /tools/agents/providers reloads don't
// flash a CMD window per-provider.
func hideConsole(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
	}
}
