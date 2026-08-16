//go:build linux || android

package oomscore

import (
	"os"
	"path/filepath"
	"strconv"
)

// procRoot is /proc in production and a temp dir under test.
var procRoot = "/proc"

// setProcRoot points the package at a different root and returns a
// restore func. Test-only, but defined here so the production path and
// the test path resolve paths through exactly the same code.
func setProcRoot(root string) func() {
	prev := procRoot
	procRoot = root
	return func() { procRoot = prev }
}

func selfPid() int { return os.Getpid() }

// Available reports whether oom_score_adj is writable for this process.
func Available() bool {
	_, err := os.Stat(filepath.Join(procRoot, strconv.Itoa(selfPid()), "oom_score_adj"))
	return err == nil
}

// Adjust writes score to /proc/<pid>/oom_score_adj.
//
// Callers treat a failure as advisory: by the time this runs the agent is
// already spawned, and refusing to run it unguarded would trade a memory
// risk for a hard outage.
func Adjust(pid int, score int) error {
	if err := validate(score); err != nil {
		return err
	}
	p := filepath.Join(procRoot, strconv.Itoa(pid), "oom_score_adj")
	return os.WriteFile(p, []byte(strconv.Itoa(score)), 0o644)
}
