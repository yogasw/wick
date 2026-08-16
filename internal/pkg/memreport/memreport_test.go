package memreport

import "testing"

// Summing a subtree is the whole point: a browser started by a tool
// started by an agent is where the memory actually is, and reading only
// the agent's own RSS reports a number wrong by an order of magnitude.
func TestSumSubtree(t *testing.T) {
	procs := []Proc{
		{PID: 1, PPID: 0, Name: "init", RSSBytes: 10},
		{PID: 100, PPID: 1, Name: "claude", RSSBytes: 150},
		{PID: 200, PPID: 100, Name: "node", RSSBytes: 200},
		{PID: 300, PPID: 200, Name: "chromium", RSSBytes: 900},
		{PID: 400, PPID: 1, Name: "codex", RSSBytes: 340},
	}

	if got := SumSubtree(procs, 100); got != 1250 {
		t.Fatalf("claude subtree = %d, want 1250 (150+200+900)", got)
	}
	if got := SumSubtree(procs, 400); got != 340 {
		t.Fatalf("codex subtree = %d, want 340", got)
	}
	if got := SumSubtree(procs, 999); got != 0 {
		t.Fatalf("unknown root = %d, want 0", got)
	}
}

// /proc is sampled without a lock, so a stale PPID can point anywhere,
// including back up the tree. The walk must terminate regardless.
func TestSumSubtree_TerminatesOnCycle(t *testing.T) {
	procs := []Proc{
		{PID: 1, PPID: 2, Name: "a", RSSBytes: 5},
		{PID: 2, PPID: 1, Name: "b", RSSBytes: 5},
	}
	done := make(chan uint64, 1)
	go func() { done <- SumSubtree(procs, 1) }()

	got := <-done
	if got != 10 {
		t.Fatalf("cyclic subtree = %d, want 10 counted once each", got)
	}
}

// The total alone does not say what to do. "claude 1.4 GB" invites
// raising a limit; "of which chromium is 1.2 GB" names the cause.
func TestLargestDescendant(t *testing.T) {
	procs := []Proc{
		{PID: 100, PPID: 1, Name: "claude", RSSBytes: 150},
		{PID: 200, PPID: 100, Name: "node", RSSBytes: 200},
		{PID: 300, PPID: 200, Name: "chromium", RSSBytes: 900},
	}
	got := LargestDescendant(procs, 100)
	if got.Name != "chromium" || got.RSSBytes != 900 {
		t.Fatalf("largest = %+v, want chromium at 900", got)
	}
}

// A leaf agent has no descendants; the caller must be able to tell that
// from "the biggest one is 0 bytes".
func TestLargestDescendant_NoChildren(t *testing.T) {
	procs := []Proc{{PID: 100, PPID: 1, Name: "codex", RSSBytes: 340}}
	if got := LargestDescendant(procs, 100); got.PID != 0 {
		t.Fatalf("largest = %+v, want the zero Proc", got)
	}
}

// Roots finds the processes worth reporting by name.
func TestRoots(t *testing.T) {
	procs := []Proc{
		{PID: 100, Name: "claude"},
		{PID: 200, Name: "node"},
		{PID: 300, Name: "codex"},
	}
	got := Roots(procs, []string{"claude", "codex"})
	if len(got) != 2 {
		t.Fatalf("found %d roots, want 2", len(got))
	}
}
