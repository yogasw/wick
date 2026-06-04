// Package env hosts small platform-detection helpers shared by app,
// daemon, and any other layer that needs to know whether the current
// process can show a GUI. Living below internal/pkg keeps these
// helpers free of higher-level dependencies (app, systemtray, ...)
// so callers don't pull in the world just to ask "do I have a screen".
package env

import (
	"os"
	"runtime"
)

// HasGUI returns true if the current process is running in an
// environment where a system tray (or any GUI) can reasonably be
// shown.
//
// Detection layers (defensive — any one signal of "headless" wins):
//
//	1. TERMUX_VERSION env       — Termux sets this; never a GUI.
//	2. GOOS == "android"        — Go for Android Termux build.
//	3. Linux without DISPLAY    — no X server, no Wayland session.
//	4. macOS over SSH           — remote session, no Aqua UI.
//	5. Anything unknown         — default to headless to avoid
//	                              hanging on a missing display.
//
// Windows + macOS desktop sessions are assumed to have a GUI.
// Headless-build users still rely on the `headless` build tag to
// strip systemtray symbols; this helper is the runtime companion.
func HasGUI() bool {
	if os.Getenv("TERMUX_VERSION") != "" {
		return false
	}
	switch runtime.GOOS {
	case "android":
		return false
	case "windows":
		return true
	case "darwin":
		return os.Getenv("SSH_CONNECTION") == ""
	case "linux":
		return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
	}
	return false
}
