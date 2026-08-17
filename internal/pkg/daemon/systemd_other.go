//go:build !linux && !android

package daemon

// systemdIsInit is always false off Linux: Windows and macOS have no
// systemd, so a claim of "via systemd" there would be wrong by
// construction.
func systemdIsInit() bool { return false }
