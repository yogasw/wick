//go:build linux || android

package memreport

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var procRoot = "/proc"

// Snapshot samples every readable process.
//
// Processes that vanish mid-walk are skipped rather than failing the
// sample: a snapshot of a moving system is expected to be slightly
// stale, not impossible.
func Snapshot() ([]Proc, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, err
	}
	var out []Proc
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue // not a pid directory
		}
		b, err := os.ReadFile(filepath.Join(procRoot, e.Name(), "status"))
		if err != nil {
			continue // exited between ReadDir and here
		}
		out = append(out, parseStatus(pid, string(b)))
	}
	return out, nil
}

// parseStatus reads the three fields the report needs out of
// /proc/<pid>/status. A kernel thread has no VmRSS line and reads as 0.
func parseStatus(pid int, body string) Proc {
	p := Proc{PID: pid}
	for _, line := range strings.Split(body, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "Name:":
			p.Name = f[1]
		case "PPid:":
			p.PPID, _ = strconv.Atoi(f[1])
		case "VmRSS:":
			kb, _ := strconv.ParseUint(f[1], 10, 64)
			p.RSSBytes = kb * 1024
		}
	}
	return p
}
