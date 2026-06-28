//go:build !linux && !android

package plugin

// applyRlimits is a no-op on platforms without portable rlimit support
// (rlimit caps ship for linux/android, the plugin host targets).
func applyRlimits() {}
