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

// Proc is one process as sampled from /proc.
type Proc struct {
	PID      int
	PPID     int
	Name     string
	RSSBytes uint64
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
