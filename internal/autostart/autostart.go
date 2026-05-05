// Package autostart registers the running binary to launch at OS user
// login. Cross-platform via per-OS files behind a shared API:
//
//	autostart.Enable(appName)   — register so the binary auto-launches at login
//	autostart.Disable(appName)  — remove the registration
//	autostart.IsEnabled(appName) — check current state
//	autostart.Path(appName)     — the OS-specific entry location, for display
//
// Mechanisms per OS:
//
//	Windows : HKCU\Software\Microsoft\Windows\CurrentVersion\Run
//	macOS   : ~/Library/LaunchAgents/<appName>.plist
//	Linux   : ~/.config/autostart/<appName>.desktop
//
// All three are user-scoped (no admin/root). The exe path written into
// each entry is resolved from os.Executable() at Enable time, so if the
// binary moves the caller should re-Enable to refresh the entry.
package autostart

import "os"

// currentExe is the resolved binary path used for new autostart entries.
// Returns "" on error — caller should treat that as "can't enable".
func currentExe() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	return exe
}
