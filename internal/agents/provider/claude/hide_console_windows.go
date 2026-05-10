//go:build windows

package claude

import (
	"os/exec"
	"syscall"
)

// hideConsole prevents the spawned claude.exe console window from
// flashing on screen when wick runs from the system tray (no parent
// console). HideWindow + CREATE_NO_WINDOW (0x08000000) — same pattern
// used by internal/systemtray/{editor,notify}_windows.go.
func hideConsole(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
	}
}
