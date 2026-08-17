package memreport

import "testing"

func rated(pid int, name string, rss uint64, cpu float64) ProcRate {
	return ProcRate{Proc: Proc{PID: pid, Name: name, RSSBytes: rss}, CPUPct: cpu}
}

// The question a per-process table cannot answer: how much is Chrome
// costing me? Eight renderers at 300 MB each read as eight modest rows
// while together they are the biggest thing on the machine.
func TestGroupBy_SumsByName(t *testing.T) {
	procs := []ProcRate{
		rated(1, "chrome.exe", 300, 5),
		rated(2, "chrome.exe", 200, 3),
		rated(3, "code.exe", 400, 1),
	}
	groups := GroupBy(procs, 0)

	var chrome ProcGroup
	for _, g := range groups {
		if g.Name == "chrome.exe" {
			chrome = g
		}
	}
	if chrome.Count != 2 {
		t.Fatalf("chrome count = %d, want 2", chrome.Count)
	}
	if chrome.RSSBytes != 500 {
		t.Fatalf("chrome RSS = %d, want 500", chrome.RSSBytes)
	}
	if chrome.CPUPct != 8 {
		t.Fatalf("chrome CPU = %v, want 8", chrome.CPUPct)
	}
}

// A share is what makes a number interpretable: 2.1 GB means something
// different on a 4 GB box than on a 64 GB one.
func TestGroupBy_PercentOfMachine(t *testing.T) {
	procs := []ProcRate{rated(1, "chrome.exe", 500, 0)}
	groups := GroupBy(procs, 2000)

	if groups[0].PctOfMachineMem != 25 {
		t.Fatalf("share = %v%%, want 25", groups[0].PctOfMachineMem)
	}
}

// Unknown machine memory must leave the share at zero rather than
// dividing by a fabricated denominator.
func TestGroupBy_UnknownMachineMemory(t *testing.T) {
	groups := GroupBy([]ProcRate{rated(1, "x", 500, 0)}, 0)
	if groups[0].PctOfMachineMem != 0 {
		t.Fatalf("share = %v, want 0 when machine memory is unknown", groups[0].PctOfMachineMem)
	}
}

// Expanding a group should put the likely culprit first.
func TestGroupBy_MembersHeaviestFirst(t *testing.T) {
	procs := []ProcRate{
		rated(1, "chrome.exe", 100, 0),
		rated(2, "chrome.exe", 900, 0),
		rated(3, "chrome.exe", 500, 0),
	}
	g := GroupBy(procs, 0)[0]

	if g.Members[0].RSSBytes != 900 || g.Members[2].RSSBytes != 100 {
		t.Fatalf("members not heaviest-first: %v %v %v",
			g.Members[0].RSSBytes, g.Members[1].RSSBytes, g.Members[2].RSSBytes)
	}
}

// A grouped table must rank by the SUM, or a browser split across many
// processes loses to a single large one it collectively dwarfs.
func TestTopGroupsBy_RanksBySum(t *testing.T) {
	procs := []ProcRate{
		rated(1, "chrome.exe", 300, 0),
		rated(2, "chrome.exe", 300, 0),
		rated(3, "chrome.exe", 300, 0),
		rated(4, "big.exe", 800, 0),
	}
	top := TopGroupsBy(GroupBy(procs, 0), GroupByMem, 5)

	if top[0].Name != "chrome.exe" {
		t.Fatalf("top = %q, want chrome.exe (900 summed beats 800)", top[0].Name)
	}
}

// Ties break on name so the table does not reshuffle between refreshes.
func TestTopGroupsBy_StableOnTies(t *testing.T) {
	procs := []ProcRate{rated(1, "b", 100, 0), rated(2, "a", 100, 0), rated(3, "c", 100, 0)}
	top := TopGroupsBy(GroupBy(procs, 0), GroupByMem, 5)

	if top[0].Name != "a" || top[1].Name != "b" || top[2].Name != "c" {
		t.Fatalf("ties did not break on name: %q %q %q", top[0].Name, top[1].Name, top[2].Name)
	}
}

// A quiet machine shows a short list, not rows of zeros.
func TestTopGroupsBy_DropsZeroTail(t *testing.T) {
	procs := []ProcRate{rated(1, "busy", 0, 40), rated(2, "idle", 0, 0)}
	top := TopGroupsBy(GroupBy(procs, 0), GroupByCPU, 10)

	if len(top) != 1 {
		t.Fatalf("got %d groups, want 1 — idle groups were not dropped", len(top))
	}
}
