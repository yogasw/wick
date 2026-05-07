//go:build windows && !headless

package systemtray

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"syscall"

	"github.com/rs/zerolog/log"
)

// notify shows an OS-level toast on Win10+.
//
// Uses NotifyIcon balloon (Shell_NotifyIcon) instead of WinRT toast: the
// WinRT path requires a registered AppUserModelID, which a portable .exe
// doesn't have, so toasts silently fail. Balloon works on every modern
// Windows without registry setup. The temporary tray icon disappears
// after the balloon timeout — runs in a separate PowerShell process so
// the real tray's event loop is untouched.
func notify(title, message string) error {
	esc := func(s string) string {
		s = strings.ReplaceAll(s, "`", "``")
		s = strings.ReplaceAll(s, `"`, "`\"")
		return s
	}
	script := fmt.Sprintf(`Add-Type -AssemblyName System.Windows.Forms;`+
		`Add-Type -AssemblyName System.Drawing;`+
		`$n=New-Object System.Windows.Forms.NotifyIcon;`+
		`$n.Icon=[System.Drawing.SystemIcons]::Information;`+
		`$n.BalloonTipTitle="%s";`+
		`$n.BalloonTipText="%s";`+
		`$n.Visible=$true;`+
		`$n.ShowBalloonTip(5000);`+
		`Start-Sleep -Seconds 6;`+
		`$n.Dispose()`,
		esc(title), esc(message))
	c := exec.Command("powershell", "-NoProfile", "-WindowStyle", "Hidden", "-Command", script)
	c.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
	}
	var stderr bytes.Buffer
	c.Stderr = &stderr
	if err := c.Start(); err != nil {
		return fmt.Errorf("start powershell: %w", err)
	}
	go func() {
		if err := c.Wait(); err != nil {
			log.Warn().Err(err).Str("stderr", stderr.String()).Msg("notify powershell exit")
		}
	}()
	return nil
}
