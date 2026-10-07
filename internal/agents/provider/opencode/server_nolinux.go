//go:build !linux

package opencode

import "os/exec"

// dieWithParent: no parent-death signal outside Linux; ShutdownServers is
// the only cleanup there.
func dieWithParent(*exec.Cmd) {}
