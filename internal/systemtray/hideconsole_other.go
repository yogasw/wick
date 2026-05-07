//go:build !windows && !headless

package systemtray

// hideConsole is a no-op outside Windows — Linux/macOS tray apps don't
// allocate a console window when launched from a desktop file / .app
// bundle, so there's nothing to detach.
func hideConsole() {}
