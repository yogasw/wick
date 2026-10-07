//go:build !linux

package omp

import "os/exec"

// dieWithParent: no parent-death signal outside Linux; ShutdownServers (omp) is
// the only cleanup there.
func dieWithParent(*exec.Cmd) {}
