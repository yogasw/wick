package wrapper

// summary.go counts a scan.
//
// Two numbers, not four. An earlier version split every count by whether
// the process descended from wick, which answered a question nobody was
// asking: a process either has a memory ceiling over it or it does not,
// and that is the same fact whether wick placed it there or a shim did.
// Once a process is in the slice its cgroup is identical either way —
// the distinction was not even observable, only inferred.
//
// Ownership still appears per row (ProcState.FromWick), because knowing
// which agent to look at is useful. It just is not what the tally is
// about.

// Summary counts a scan by the only thing that changes the outcome:
// whether a ceiling applies.
type Summary struct {
	Total int
	// Isolated sits inside the agents slice, so the per-scope and
	// aggregate ceilings both reach it. Placed there by wick at spawn, or
	// by the path shim — indistinguishable from outside, and equivalent
	// in effect.
	Isolated int
	// Unisolated has no ceiling over it at all. This is the number that
	// matters: these processes share the machine's memory and nothing
	// configured here can stop one of them taking the rest of it.
	Unisolated int
}

// Summarize tallies a scan.
func Summarize(procs []ProcState) Summary {
	var s Summary
	for _, p := range procs {
		s.Total++
		if p.Isolated {
			s.Isolated++
			continue
		}
		s.Unisolated++
	}
	return s
}
