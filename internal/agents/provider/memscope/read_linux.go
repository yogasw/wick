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

// ReadStats reports what the kernel recorded for a scope. Tries the
// systemd (cgroup v2) path first, then the cgroupfs (v1) path — cheap to
// try both since a given wick process only ever wrapped through one
// backend, so exactly one of the two ever finds a real scope.
func ReadStats(unit string) Stats {
	if st := ReadStatsAt(scopeSearchRoot(), unit); st.Known {
		return st
	}
	return ReadStatsV1At(cgroupV1MemoryRoot, SliceName, unit)
}

// ReadSliceOOM reads the agents slice's own oom counter on the running
// system. Only meaningful on the v2 path — cgroup v1 has no per-group
// oom event counter, and 0 there degrades to the host-OOM verdict.
func ReadSliceOOM() int {
	return ReadSliceOOMAt(scopeSearchRoot())
}

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
