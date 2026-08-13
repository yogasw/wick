//go:build !windows

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// findOwnedBrowserPIDs returns every process whose command line points at a
// user-data dir under root — the ONLY reliable ownership marker for a browser
// wick launched.
//
// Why not the recorded PID: sessionMeta.PID is the process we spawned, but a
// browser is a process TREE it builds itself. Chrome forks renderer, GPU and
// utility children that appear in no metadata; Firefox may fork and let the
// original PID exit, so the recorded PID can even be the wrong one. Matching on
// --user-data-dir catches every one of them, whatever engine made them, without
// needing to know how that engine structures its processes.
//
// Scoping to root is what makes this safe to run: a browser the user launched
// themselves has its user-data dir somewhere else entirely, so it can never match.
func findOwnedBrowserPIDs(root string) []ownedProc {
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	prefix := "--user-data-dir=" + root
	self := os.Getpid()
	var out []ownedProc
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 || pid == self {
			continue // not a pid dir, or ourselves
		}
		// /proc/<pid>/cmdline is NUL-separated, one argv entry per field, so
		// the flag can be matched exactly rather than by substring.
		raw, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue // process exited between ReadDir and here, or unreadable
		}
		for _, arg := range bytes.Split(raw, []byte{0}) {
			s := string(arg)
			if strings.HasPrefix(s, prefix) {
				out = append(out, ownedProc{PID: pid, UserDataDir: strings.TrimPrefix(s, "--user-data-dir=")})
				break
			}
		}
	}
	return out
}
