//go:build !linux && !android

package wrapper

import "errors"

// status_other.go stubs the scan off Linux, where there is no /proc to
// read cgroup membership from.

// ErrUnsupported reports that this platform has no cgroup membership to
// inspect.
var ErrUnsupported = errors.New("agent isolation status requires Linux")

// Scan reports nothing off Linux.
func Scan(names []string, selfPID int, sliceName string) ([]ProcState, error) {
	return nil, ErrUnsupported
}
