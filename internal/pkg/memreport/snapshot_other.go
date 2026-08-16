//go:build !linux && !android

package memreport

import "errors"

// ErrUnsupported reports that this platform has no /proc to sample.
//
// Callers print it and exit 0: a report command that errors out is worse
// than one that says why it cannot help.
var ErrUnsupported = errors.New("process memory reporting requires Linux")

// Snapshot reports nothing off Linux.
func Snapshot() ([]Proc, error) { return nil, ErrUnsupported }
