//go:build !linux && !android

package memscope

import "errors"

// ErrUnsupported is the documented degraded path off Linux, not a failure.
var ErrUnsupported = errors.New("systemd scopes not supported on this platform")

// EnsureSlice is a no-op off Linux, where there is no systemd to tell.
func EnsureSlice(limits SliceLimits) error { return ErrUnsupported }

// Available always reports false off Linux.
func Available() bool { return false }
