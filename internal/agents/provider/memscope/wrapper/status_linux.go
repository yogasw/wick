//go:build linux || android

package wrapper

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/yogasw/wick/internal/pkg/memreport"
)

// status_linux.go answers "what is actually running, and is it
// contained?" — for every matching process on the machine, not only the
// ones wick spawned.
//
// That scope is the point. A report that counts only its own successes
// reads as "all contained" while an identical binary runs unguarded
// under another service: same program, same memory, same machine, and
// the aggregate ceiling on agents.slice does not reach it. The operator
// asking "why did the box run out of memory" needs the processes nobody
// installed a shim for.

// Scan reports every process named after one of the given providers.
//
// selfPID is wick's own pid; descent is resolved through the snapshot's
// parent links rather than by name, so a differently-named parent in the
// chain does not break attribution.
func Scan(names []string, selfPID int, sliceName string) ([]ProcState, error) {
	procs, err := memreport.Snapshot()
	if err != nil {
		return nil, err
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	parent := make(map[int]int, len(procs))
	for _, p := range procs {
		parent[p.PID] = p.PPID
	}

	var out []ProcState
	for _, p := range procs {
		// BaseName, not the raw name: Windows reports "claude.exe" where
		// Linux reports "claude", and one provider list has to match on
		// both. Shared with memreport.Roots so the two cannot drift.
		if !want[memreport.BaseName(p.Name)] {
			continue
		}
		cg := readCgroup(p.PID)
		out = append(out, ProcState{
			PID:      p.PID,
			Name:     p.Name,
			RSSBytes: p.RSSBytes,
			Cgroup:   cg,
			Isolated: strings.Contains(cg, sliceName),
			FromWick: descendsFrom(parent, p.PID, selfPID),
		})
	}
	return out, nil
}

// descendsFrom walks parent links up from pid looking for ancestor.
//
// Bounded because /proc is sampled over time rather than atomically: a
// pid recycled mid-walk can produce a cycle, and an unbounded loop here
// would hang the status command rather than misreport one row.
func descendsFrom(parent map[int]int, pid, ancestor int) bool {
	for i := 0; i < 64 && pid > 1; i++ {
		if pid == ancestor {
			return true
		}
		next, ok := parent[pid]
		if !ok || next == pid {
			return false
		}
		pid = next
	}
	return false
}

// readCgroup returns the leaf cgroup path for a pid, or "" when it
// cannot be read — a process owned by another user, or one that exited
// between the snapshot and this call.
func readCgroup(pid int) string {
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cgroup"))
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	last := lines[len(lines)-1]
	// Both v1 ("N:controller:/path") and v2 ("0::/path") put the path
	// after the last colon.
	if i := strings.LastIndex(last, ":"); i >= 0 {
		return last[i+1:]
	}
	return last
}
