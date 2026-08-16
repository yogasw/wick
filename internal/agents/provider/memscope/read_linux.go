//go:build linux || android

package memscope

import (
	"os"
	"path"
	"path/filepath"
	"strings"
)

// cgroupRoot is the cgroup v2 mount point.
var cgroupRoot = "/sys/fs/cgroup"

// ReadStats reports what the kernel recorded for a scope.
func ReadStats(unit string) Stats { return ReadStatsAt(scopeSearchRoot(), unit) }

// scopeSearchRoot resolves the directory the agents slice sits in.
//
// The path embeds the uid (user@1000.service), so it is derived from this
// process's own cgroup rather than hardcoded: wick runs as whichever user
// installed it, never a fixed one.
func scopeSearchRoot() string {
	body, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return cgroupRoot
	}
	// cgroup v2 reports a single "0::<path>" line.
	for _, line := range strings.Split(string(body), "\n") {
		p, ok := strings.CutPrefix(strings.TrimSpace(line), "0::")
		if !ok {
			continue
		}
		// Walk up to the user manager that owns the user-scope slices.
		for dir := p; dir != "/" && dir != "." && dir != ""; dir = path.Dir(dir) {
			base := path.Base(dir)
			if strings.HasPrefix(base, "user@") && strings.HasSuffix(base, ".service") {
				return filepath.Join(cgroupRoot, filepath.FromSlash(dir))
			}
		}
	}
	return cgroupRoot
}
