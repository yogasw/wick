//go:build !windows

// Package procgroup starts agent subprocesses as the root of their own
// process group, so teardown can reach the descendants they spawn (MCP
// servers, shells, tool subprocesses) instead of orphaning them.
//
// Shared by every provider spawner rather than copied into each: three
// private implementations of the same rule would drift, and the one that
// drifted would leak processes silently.
package procgroup

import (
	"os/exec"
	"syscall"
)

// Apply marks cmd as the leader of a new process group.
//
// Merges into any existing SysProcAttr rather than replacing it: spawners
// set other fields there, and overwriting the struct would silently drop
// them.
func Apply(c *exec.Cmd) {
	if c == nil {
		return
	}
	if c.SysProcAttr == nil {
		c.SysProcAttr = &syscall.SysProcAttr{}
	}
	c.SysProcAttr.Setpgid = true
}
