//go:build windows

package main

import "os"

// processAlive reports whether a PID is still running. Used to tell a live
// browser from a stale profile lock left behind by a killed one.
//
// Unlike Unix, os.FindProcess on Windows actually opens the process handle and
// fails for a PID that no longer exists — so the lookup itself is the test.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	_, err := os.FindProcess(pid)
	return err == nil
}
