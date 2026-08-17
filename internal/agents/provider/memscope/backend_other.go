//go:build !linux && !android

package memscope

// backend_other.go gives the platform-agnostic callers (memguard.go) the
// same vocabulary off Linux, where neither mechanism Backend describes
// exists.

// Backend identifies which isolation mechanism is in play. See
// backend_linux.go for what each value means; off Linux DetectBackend
// only ever returns BackendNone. BackendSystemd and BackendCgroupFS still
// need to exist here so memguard.go's backend switch — shared by every
// platform — compiles; they are simply unreachable off Linux.
type Backend int

const (
	BackendNone Backend = iota
	BackendSystemd
	BackendCgroupFS
)

func (b Backend) String() string { return "none" }

// DetectBackend always reports BackendNone off Linux.
func DetectBackend() Backend { return BackendNone }
