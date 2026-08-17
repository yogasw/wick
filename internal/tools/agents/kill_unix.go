//go:build !windows

package agents

import (
	"os"
	"syscall"
)

// terminate asks a process to close.
//
// SIGTERM, not SIGKILL: a browser or an editor given the chance will flush
// its state and close its files, where an outright kill can leave a
// corrupt profile behind. A process that ignores SIGTERM can be dealt with
// from the operator's own shell — that escalation is their call, not a
// dashboard's.
func terminate(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(syscall.SIGTERM)
}
