package memreport

// owner.go answers "whose is this process" for a whole snapshot at once.
//
// /proc knows the OS user, which on a wick host is the same account for
// everything and therefore says nothing. The question an operator actually
// asks — which PERSON's spawn is this — is only answerable by wick: the pool
// knows which pid it started for which caller. That gives the root of each
// agent tree; everything below it (the MCP servers, the shells, the browser a
// tool opened) inherits, because a child was started by the agent and belongs
// to the same person.

// Owners labels every process in each root's subtree with that root's label.
// Processes outside every root are absent from the result rather than mapped
// to an empty string, so a caller can tell "nobody's" from "unnamed".
//
// A root inside another root's subtree keeps its OWN label and its descendants
// take it too: the nearest enclosing root wins. That is not a hypothetical
// tidiness — a sub-agent spawned for a different caller sits under its
// parent's tree, and attributing its memory to the parent's owner would name
// the wrong person.
func Owners(procs []Proc, roots map[int]string) map[int]string {
	if len(roots) == 0 {
		return map[int]string{}
	}
	children := make(map[int][]int, len(procs))
	present := make(map[int]bool, len(procs))
	for _, p := range procs {
		children[p.PPID] = append(children[p.PPID], p.PID)
		present[p.PID] = true
	}

	out := make(map[int]string, len(procs))
	for root, label := range roots {
		if !present[root] {
			continue // already exited between the pool read and the scan
		}
		// Same cycle guard as the other walkers here: /proc is sampled
		// without a lock, so a reused PID can produce a parent link that
		// loops back on itself.
		visited := map[int]bool{}
		stack := []int{root}
		for len(stack) > 0 {
			pid := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if visited[pid] {
				continue
			}
			visited[pid] = true
			out[pid] = label
			for _, kid := range children[pid] {
				// Another root owns itself and everything under it.
				if _, isRoot := roots[kid]; isRoot {
					continue
				}
				stack = append(stack, kid)
			}
		}
	}
	return out
}
