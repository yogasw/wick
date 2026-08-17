package memreport

import "sort"

// group.go collapses processes by executable name.
//
// A browser is not one process — Chrome runs a dozen, Code runs several,
// and a per-process table shows each of them separately while answering
// none of "how much is Chrome costing me". Ranking individual PIDs is
// still useful for finding the ONE renderer that has run away, so both
// views exist and the UI switches between them.

// ProcGroup is every process sharing one executable name, summed.
type ProcGroup struct {
	Name  string
	Count int
	// PIDs of the members, heaviest first, so a caller can expand a group
	// into its parts without a second pass over the snapshot.
	Members []ProcRate

	RSSBytes   uint64
	CPUPct     float64
	IOReadBps  uint64
	IOWriteBps uint64

	// PctOfTotal is this group's share of a caller-supplied total. Filled
	// by GroupBy when a total is given, because the share is what makes a
	// number interpretable: "2.1 GB" means nothing until you know whether
	// the machine has 4 GB or 64.
	PctOfMachineMem float64
}

// GroupBy collapses rated processes by name.
//
// machineMemBytes may be 0 when the machine's memory is unknown; the
// share is then left at zero rather than computed against a fabricated
// denominator.
func GroupBy(procs []ProcRate, machineMemBytes uint64) []ProcGroup {
	byName := map[string]*ProcGroup{}
	for _, p := range procs {
		g, ok := byName[p.Name]
		if !ok {
			g = &ProcGroup{Name: p.Name}
			byName[p.Name] = g
		}
		g.Count++
		g.Members = append(g.Members, p)
		g.RSSBytes += p.RSSBytes
		g.CPUPct += p.CPUPct
		g.IOReadBps += p.IOReadBps
		g.IOWriteBps += p.IOWriteBps
	}

	out := make([]ProcGroup, 0, len(byName))
	for _, g := range byName {
		if machineMemBytes > 0 {
			g.PctOfMachineMem = float64(g.RSSBytes) / float64(machineMemBytes) * 100
		}
		// Members heaviest first so expanding a group puts the likely
		// culprit at the top.
		sort.Slice(g.Members, func(i, j int) bool {
			if g.Members[i].RSSBytes != g.Members[j].RSSBytes {
				return g.Members[i].RSSBytes > g.Members[j].RSSBytes
			}
			return g.Members[i].PID < g.Members[j].PID
		})
		out = append(out, *g)
	}
	return out
}

// TopGroupsBy ranks groups and returns the first limit, dropping the
// zero-valued tail. Ties break on name so the order is stable between
// refreshes.
func TopGroupsBy(groups []ProcGroup, key func(ProcGroup) float64, limit int) []ProcGroup {
	out := make([]ProcGroup, len(groups))
	copy(out, groups)

	sort.Slice(out, func(i, j int) bool {
		a, b := key(out[i]), key(out[j])
		if a != b {
			return a > b
		}
		return out[i].Name < out[j].Name
	})

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

// Ranking keys for TopGroupsBy.
func GroupByMem(g ProcGroup) float64 { return float64(g.RSSBytes) }
func GroupByCPU(g ProcGroup) float64 { return g.CPUPct }
func GroupByIO(g ProcGroup) float64  { return float64(g.IOReadBps + g.IOWriteBps) }
