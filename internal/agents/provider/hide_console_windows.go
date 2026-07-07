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
