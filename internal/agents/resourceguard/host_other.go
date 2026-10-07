//go:build !linux

package resourceguard

// NewHost returns nil off Linux: there is no cgroup v2 tree or PSI to
// read, and the guard's Run is a no-op on a nil host.
func NewHost() Host { return nil }
