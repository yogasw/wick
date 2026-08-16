//go:build !linux && !android

package sysmem

// Total reports unknown off Linux.
func Total() (uint64, bool) { return 0, false }

// Available reports unknown off Linux.
//
// Callers must admit the spawn on an unknown reading rather than block
// it: an advisory guard that cannot read the machine must not become a
// spawn ban.
func Available() (uint64, bool) { return 0, false }
