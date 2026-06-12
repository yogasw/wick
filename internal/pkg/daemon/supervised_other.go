//go:build !linux && !android

package daemon

// On Windows / macOS there is no systemd-user backend (autostart goes
// through the GUI login-item path in service.go), so the daemon CLI
// always uses the PID-file jalur. These stubs let daemon_cmd.go call
// the same helpers unconditionally.

func ServiceManaged(appName string) bool    { return false }
func ServiceActive(appName string) bool     { return false }
func ServiceCtl(appName, verb string) error { return nil }
