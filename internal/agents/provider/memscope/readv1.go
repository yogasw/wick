package memscope

import (
	"os"
	"path/filepath"
)

// readv1.go parses cgroup v1 memory-controller files — the format the
// cgroupfs fallback writes into, as opposed to read.go's v2
// memory.events/memory.peak that a systemd scope produces. Deliberately
// untagged, like read.go, so the parsing is exercised on every platform's
// test run against a temp tree, not only on the Linux box that can
// produce the real files.
//
// v1 has no counterpart to v2's memory.events oom_kill — no per-group
// counter survives a kill the way cgroup v2's does. So ReadStatsV1At
// reports Known=true (a real peak was read) but always OOMKills=0: it can
// say how much memory a scope used, never confirm a kill caused its exit.
// ClassifyExit already treats OOMKills==0 as "no evidence, not an OOM" —
// exactly the honest answer here, not a guess dressed as a negative.
// Reporting a fabricated kill count would be worse than reporting none.

// ReadStatsV1At reports what a cgroup v1 scope's files say, rooted at an
// explicit directory. Production calls ReadStatsCgroupFS, which resolves
// the real host root; tests call this against a temp tree, the same split
// ReadStatsAt/ReadStats uses for the v2 path.
func ReadStatsV1At(root, slice, unit string) Stats {
	dir := filepath.Join(root, slice, unit+".scope")
	// memory.max_usage_in_bytes is v1's running peak — analogous to v2's
	// memory.peak, but it is present from cgroup creation (starts at 0)
	// rather than appearing only once something has run, so its mere
	// presence is not evidence the scope ever held a process. Require it
	// to be readable at all as the "this scope is real" signal, same role
	// memory.events plays for the v2 reader.
	peak, err := os.ReadFile(filepath.Join(dir, "memory.max_usage_in_bytes"))
	if err != nil {
		return Stats{}
	}
	return Stats{Known: true, PeakBytes: parseUint(string(peak))}
}
