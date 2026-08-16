//go:build !linux && !android

package oomscore

import (
	"errors"
	"os"
)

// ErrUnsupported is returned on platforms with no oom_score_adj. Callers
// log it at debug and carry on — this is the documented degraded path,
// not a failure.
var ErrUnsupported = errors.New("oom_score_adj not supported on this platform")

var procRoot = ""

// setProcRoot mirrors the Linux seam so shared test helpers compile on
// every platform.
func setProcRoot(root string) func() {
	prev := procRoot
	procRoot = root
	return func() { procRoot = prev }
}

func selfPid() int { return os.Getpid() }

// Available always reports false off Linux.
func Available() bool { return false }

// Adjust validates its argument, then reports the platform has no such knob.
func Adjust(pid int, score int) error {
	if err := validate(score); err != nil {
		return err
	}
	return ErrUnsupported
}
