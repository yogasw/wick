//go:build !windows

package main

import (
	"os"
	"syscall"
)

// processAlive reports whether a PID is still running. Used to tell a live
// browser from a stale profile lock left behind by a killed one.
//
// os.FindProcess never fails on Unix, so signal 0 is the real test: it runs the
// kernel's existence + permission check without delivering anything.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
