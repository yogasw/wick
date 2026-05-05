//go:build windows && !headless

package systemtray

import (
	"os/exec"
	"syscall"
)

// openInEditor opens path in the user's default app for that file
// type. Uses cmd.exe /c start as the launcher, with HideWindow set on
// the cmd wrapper so users don't see a brief console flash before the
// real editor takes over.
//
// CreationFlags 0x08000000 = CREATE_NO_WINDOW — belt-and-suspenders for
// some Windows builds where HideWindow alone still leaks a flicker.
func openInEditor(path string) error {
	c := exec.Command("cmd", "/c", "start", "", path)
	c.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
	}
	return c.Start()
}
