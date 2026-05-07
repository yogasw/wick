//go:build windows && !headless

package systemtray

import (
	"bytes"
	"os/exec"
	"syscall"

	"github.com/rs/zerolog/log"
)

// openInEditor opens path in the user's default app for that file
// type. Uses cmd.exe /c start as the launcher, with HideWindow set on
// the cmd wrapper so users don't see a brief console flash before the
// real editor takes over.
//
// CreationFlags 0x08000000 = CREATE_NO_WINDOW — belt-and-suspenders for
// some Windows builds where HideWindow alone still leaks a flicker.
//
// Wait + stderr capture so a silent failure (path missing, default app
// not registered, etc.) lands in the log instead of disappearing.
func openInEditor(path string) error {
	c := exec.Command("cmd", "/c", "start", "", path)
	c.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
	}
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if err := c.Start(); err != nil {
		return err
	}
	go func() {
		if err := c.Wait(); err != nil {
			log.Warn().
				Str("path", path).
				Err(err).
				Str("stderr", stderr.String()).
				Msg("openInEditor: cmd exited non-zero")
		}
	}()
	return nil
}
