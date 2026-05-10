//go:build !linux

package updater

import "errors"

// swapLinuxDeb is a build stub for non-Linux platforms — the .deb
// branch in ApplyStagedAndRestart is gated by GOOS check + asset
// suffix, so this should never actually run, but it must exist for
// the package to build on Windows/macOS.
func swapLinuxDeb(_, _, _ string, _ Sentinel) error {
	return errors.New("swapLinuxDeb called on non-linux build")
}
