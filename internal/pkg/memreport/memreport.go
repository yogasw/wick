// Package memreport summarises process memory by subtree.
//
// It exists so an operator can see who is actually using memory BEFORE
// enabling any limit — it reads /proc and needs no cgroup, no systemd,
// and no configuration change. Less precise than cgroup accounting
// (shared pages are counted once per process that maps them), but precise
// enough to choose a ceiling, and available where cgroups are not.
//
// Distinct from pkg/proctree, which kills process trees. This one only
// measures them.
package memreport

import "sort"

// Proc is one process as sampled from /proc.
//
// CPUTicks and IO counters are CUMULATIVE since the process started, not
// rates. A rate needs two samples and the elapsed time between them —
// see CPUPercent — because /proc has no instantaneous "current CPU%".
type Proc struct {
	PID      int
	PPID     int
	Name     string
	RSSBytes uint64
	// CPUTicks is utime+stime in clock ticks since process start.
	CPUTicks uint64
	// IOReadBytes / IOWriteBytes are read_bytes/write_bytes from
	// /proc/<pid>/io — actual block-device traffic, not page-cache hits.
	// Both are 0 when the file is unreadable, which is normal for
	// processes owned by another user.
	IOReadBytes  uint64
	IOWriteBytes uint64
}

// clockTicksPerSec is the kernel's USER_HZ. It is 100 on every Linux
// platform Go supports; reading it properly needs cgo (sysconf), which
// this package deliberately avoids.
const clockTicksPerSec = 100

// CPUPercent converts a tick delta over an elapsed period into percent of
// one core. 100 means one core fully busy; 250 means two and a half.
//
// Returns 0 for a non-positive elapsed time rather than dividing by zero:
// two samples from the same instant carry no rate information.
func CPUPercent(ticksDelta uint64, elapsedSec float64) float64 {
	if elapsedSec <= 0 {
		return 0
	}
	return (float64(ticksDelta) / clockTicksPerSec) / elapsedSec * 100
}

// SumSubtree totals RSS for root and every descendant.
//
// Descendants are the point: a browser started by a tool started by an
// agent is where the memory actually is, and reading only the agent's own
// RSS reports a number wrong by an order of magnitude.
//
// Visited-tracking is not defensive dressing: /proc is sampled without a
// lock, so a process can exit and its PID be reused mid-walk, producing
// parent links that form a cycle. Without it the walk would not terminate.
func SumSubtree(procs []Proc, root int) uint64 {
	children := make(map[int][]Proc, len(procs))
	self := make(map[int]Proc, len(procs))
	for _, p := range procs {
		children[p.PPID] = append(children[p.PPID], p)
		self[p.PID] = p
	}
	if _, ok := self[root]; !ok {
		return 0
	}

	visited := make(map[int]bool, len(procs))
	var total uint64
	stack := []int{root}
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[pid] {
			continue
		}
		visited[pid] = true
		total += self[pid].RSSBytes
		for _, c := range children[pid] {
			if !visited[c.PID] {
				stack = append(stack, c.PID)
			}
		}
	}
	return total
}

// LargestDescendant returns the heaviest process strictly below root, or
// a zero Proc when root has no descendants.
//
// Reported alongside the subtree total because the total alone does not
// say what to do: "claude 1.4 GB" invites raising a limit, while "claude
// 1.4 GB, of which chromium is 1.2 GB" points at the actual cause.
func LargestDescendant(procs []Proc, root int) Proc {
	children := make(map[int][]Proc, len(procs))
	for _, p := range procs {
		children[p.PPID] = append(children[p.PPID], p)
	}

	var best Proc
	visited := map[int]bool{root: true}
	stack := append([]int{}, root)
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, c := range children[pid] {
			if visited[c.PID] {
				continue
			}
			visited[c.PID] = true
			if c.RSSBytes > best.RSSBytes {
				best = c
			}
			stack = append(stack, c.PID)
		}
	}
	return best
}

// Totals is every resource for one subtree, summed.
type Totals struct {
	RSSBytes     uint64
	CPUTicks     uint64
	IOReadBytes  uint64
	IOWriteBytes uint64
	// Procs counts processes in the subtree. A fork bomb shows up here
	// long before it shows up in RSSBytes — thousands of tiny processes
	// stay under every memory ceiling while crippling the scheduler.
	Procs int
}

// SumSubtreeAll totals every resource for root and its descendants in one
// walk, so a sampler does not traverse the same tree four times.
//
// Same cycle-safety as SumSubtree: /proc is sampled without a lock, so a
// reused PID can produce parent links that form a loop.
func SumSubtreeAll(procs []Proc, root int) Totals {
	children := make(map[int][]Proc, len(procs))
	self := make(map[int]Proc, len(procs))
	for _, p := range procs {
		children[p.PPID] = append(children[p.PPID], p)
		self[p.PID] = p
	}
	if _, ok := self[root]; !ok {
		return Totals{}
	}

	var t Totals
	visited := make(map[int]bool, len(procs))
	stack := []int{root}
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[pid] {
			continue
		}
		visited[pid] = true
		p := self[pid]
		t.RSSBytes += p.RSSBytes
		t.CPUTicks += p.CPUTicks
		t.IOReadBytes += p.IOReadBytes
		t.IOWriteBytes += p.IOWriteBytes
		t.Procs++
		for _, c := range children[pid] {
			if !visited[c.PID] {
				stack = append(stack, c.PID)
			}
		}
	}
	return t
}

// Subtree returns every process in root's tree, heaviest first, capped at
// limit (0 = uncapped).
//
// This is the task-manager view: the aggregate in Totals answers "how much
// is this agent using", while the list answers "which process is using
// it" — the question an operator actually acts on. Sorted by RSS because
// that is what a limit is set against; ties break on PID so repeated calls
// against one snapshot render in a stable order rather than shuffling
// between refreshes.
//
// The cap matters: an agent driving a browser can have dozens of renderer
// processes, and an uncapped list would grow the API payload without
// telling the operator anything the top entries did not.
func Subtree(procs []Proc, root int, limit int) []Proc {
	children := make(map[int][]Proc, len(procs))
	self := make(map[int]Proc, len(procs))
	for _, p := range procs {
		children[p.PPID] = append(children[p.PPID], p)
		self[p.PID] = p
	}
	if _, ok := self[root]; !ok {
		return nil
	}

	// Same cycle guard as SumSubtree: /proc is sampled without a lock, so
	// a reused PID can produce parent links that loop.
	var out []Proc
	visited := make(map[int]bool, len(procs))
	stack := []int{root}
	for len(stack) > 0 {
		pid := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[pid] {
			continue
		}
		visited[pid] = true
		out = append(out, self[pid])
		for _, c := range children[pid] {
			if !visited[c.PID] {
				stack = append(stack, c.PID)
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].RSSBytes != out[j].RSSBytes {
			return out[i].RSSBytes > out[j].RSSBytes
		}
		return out[i].PID < out[j].PID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// TopBy ranks every process on the machine and returns the first limit.
//
// Machine-wide on purpose, and distinct from Subtree: an operator asking
// "why is this box slow" needs the answer even when no agent is running,
// and the culprit is frequently not an agent at all. Subtree attributes
// usage to a session; this attributes it to the machine.
//
// key selects the dimension. Ties break on PID so repeated calls against
// one snapshot render in a stable order rather than shuffling between
// refreshes.
func TopBy(procs []Proc, key func(Proc) uint64, limit int) []Proc {
	out := make([]Proc, len(procs))
	copy(out, procs)

	sort.Slice(out, func(i, j int) bool {
		a, b := key(out[i]), key(out[j])
		if a != b {
			return a > b
		}
		return out[i].PID < out[j].PID
	})
	// Drop the tail of zero-valued processes: a list padded with idle
	// system processes at 0 B says nothing, and on a quiet machine it
	// would fill the whole table.
	end := len(out)
	for end > 0 && key(out[end-1]) == 0 {
		end--
	}
	out = out[:end]

	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// ByRSS / ByCPUTicks / ByIO are the ranking keys for TopBy.
func ByRSS(p Proc) uint64      { return p.RSSBytes }
func ByCPUTicks(p Proc) uint64 { return p.CPUTicks }
func ByIO(p Proc) uint64       { return p.IOReadBytes + p.IOWriteBytes }

// Roots returns processes whose name matches any of names.
func Roots(procs []Proc, names []string) []Proc {
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[n] = true
	}
	var out []Proc
	for _, p := range procs {
		if want[p.Name] {
			out = append(out, p)
		}
	}
	return out
}
