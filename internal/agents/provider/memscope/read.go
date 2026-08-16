package memscope

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// read.go holds the cgroup-file parsing, deliberately untagged so it is
// exercised by the test suite on every platform. Only the resolution of
// WHERE the real slice lives is platform-specific.

// ReadStatsAt reports what a scope's cgroup files say, rooted at an
// explicit directory. Production calls ReadStats, which resolves the root
// for the running system; tests call this against a temp tree.
func ReadStatsAt(root, unit string) Stats {
	dir := filepath.Join(root, SliceName, unit+".scope")
	ev, err := os.ReadFile(filepath.Join(dir, "memory.events"))
	if err != nil {
		// Reaped, or never existed. Either way: no evidence, no verdict.
		return Stats{}
	}
	st := Stats{Known: true, OOMKills: parseEventCount(string(ev), "oom_kill")}
	if peak, err := os.ReadFile(filepath.Join(dir, "memory.peak")); err == nil {
		st.PeakBytes = parseUint(string(peak))
	}
	return st
}

// parseEventCount pulls one counter out of a flat "key value" file,
// tolerating unknown keys, short lines, and non-numeric values — kernel
// files gain fields between versions and must never panic a reader.
func parseEventCount(body, key string) int {
	for _, line := range strings.Split(body, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || f[0] != key {
			continue
		}
		n, err := strconv.Atoi(f[1])
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

func parseUint(s string) uint64 {
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0
	}
	return n
}
