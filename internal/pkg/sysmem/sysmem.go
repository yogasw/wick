// Package sysmem reports machine memory.
//
// It reads /proc/meminfo on Linux and reports "unknown" elsewhere, so
// callers must handle absence rather than assume a number. Needs no
// cgroup, no systemd, and no privileges — which is why it still works on
// Termux/Android, where scope isolation does not.
package sysmem

import (
	"strconv"
	"strings"
)

// parseMeminfo extracts MemTotal and MemAvailable as bytes. A missing
// field yields 0, which callers read as unknown.
//
// MemAvailable, not MemFree: MemFree excludes reclaimable page cache, so
// using it would refuse spawns on a machine that is entirely healthy.
func parseMeminfo(body string) (total, available uint64) {
	for _, line := range strings.Split(body, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		v, err := strconv.ParseUint(f[1], 10, 64)
		if err != nil {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			total = v * 1024
		case "MemAvailable:":
			available = v * 1024
		}
	}
	return total, available
}
