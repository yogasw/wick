//go:build windows

// Package procgroup starts agent subprocesses as the root of their own
// process group, so teardown can reach the descendants they spawn (MCP
// servers, shells, tool subprocesses) instead of orphaning them.
//
// Windows is wick's primary platform and is treated as a first-class
// target here: it gets the same guarantee as POSIX via
// CREATE_NEW_PROCESS_GROUP plus `taskkill /T` at teardown, rather than
// being skipped because it lacks process groups in the POSIX sense.
package procgroup

import (
	"os/exec"
	"syscall"
)

// createNewProcessGroup = CREATE_NEW_PROCESS_GROUP.
const createNewProcessGroup = 0x00000200

// Apply marks cmd as the root of a new process group.
//
// ORs the flag into CreationFlags instead of assigning: spawners already
// set CREATE_NO_WINDOW there to stop a console flashing on screen, and
// overwriting it would bring that flash back.
func Apply(c *exec.Cmd) {
	if c == nil {
		return
	}
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.CreationFlags |= createNewProcessGroup
}
