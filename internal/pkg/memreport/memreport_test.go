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

// The task-manager view: which processes, heaviest first. The aggregate
// says how much an agent uses; this says what to look at.
func TestSubtree_HeaviestFirst(t *testing.T) {
	procs := []Proc{
		{PID: 1, PPID: 0, Name: "init", RSSBytes: 10},
		{PID: 100, PPID: 1, Name: "claude", RSSBytes: 150},
		{PID: 200, PPID: 100, Name: "node", RSSBytes: 200},
		{PID: 300, PPID: 200, Name: "chromium", RSSBytes: 900},
	}

	got := Subtree(procs, 100, 0)
	if len(got) != 3 {
		t.Fatalf("got %d processes, want 3 (init is not in the subtree)", len(got))
	}
	if got[0].Name != "chromium" || got[1].Name != "node" || got[2].Name != "claude" {
		t.Fatalf("wrong order: %v", []string{got[0].Name, got[1].Name, got[2].Name})
	}
}

// The cap keeps a browser's dozens of renderers from bloating the payload
// without adding information.
func TestSubtree_RespectsLimit(t *testing.T) {
	procs := []Proc{{PID: 100, PPID: 1, Name: "claude", RSSBytes: 100}}
	for i := 0; i < 30; i++ {
		procs = append(procs, Proc{PID: 200 + i, PPID: 100, Name: "renderer", RSSBytes: uint64(i)})
	}

	got := Subtree(procs, 100, 5)
	if len(got) != 5 {
		t.Fatalf("got %d processes, want the limit of 5", len(got))
	}
	// And the cap must keep the HEAVIEST, not an arbitrary five.
	if got[0].RSSBytes != 100 {
		t.Fatalf("capped list dropped the heaviest process: %+v", got[0])
	}
}

// Equal sizes must not shuffle between refreshes — a table that reorders
// itself on every poll is unreadable.
func TestSubtree_StableOnTies(t *testing.T) {
	procs := []Proc{
		{PID: 100, PPID: 1, Name: "claude", RSSBytes: 50},
		{PID: 300, PPID: 100, Name: "a", RSSBytes: 50},
		{PID: 200, PPID: 100, Name: "b", RSSBytes: 50},
	}
	first := Subtree(procs, 100, 0)
	second := Subtree(procs, 100, 0)
	for i := range first {
		if first[i].PID != second[i].PID {
			t.Fatalf("order is not stable across calls: %v vs %v", first, second)
		}
	}
	if first[0].PID != 100 || first[1].PID != 200 || first[2].PID != 300 {
		t.Fatalf("ties did not break on PID: %v", first)
	}
}

// An unknown root is not an error, just nothing to show.
func TestSubtree_UnknownRoot(t *testing.T) {
	if got := Subtree([]Proc{{PID: 1}}, 999, 0); got != nil {
		t.Fatalf("unknown root returned %v, want nil", got)
	}
}

// A cyclic parent link (PID reuse mid-walk) must not hang the listing.
func TestSubtree_TerminatesOnCycle(t *testing.T) {
	procs := []Proc{
		{PID: 1, PPID: 2, Name: "a", RSSBytes: 5},
		{PID: 2, PPID: 1, Name: "b", RSSBytes: 5},
	}
	done := make(chan int, 1)
	go func() { done <- len(Subtree(procs, 1, 0)) }()
	if n := <-done; n != 2 {
		t.Fatalf("cyclic subtree listed %d processes, want 2 counted once each", n)
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

// Windows reports "claude.exe" where Linux reports "claude", and callers
// name providers the way a person types them. Matching the raw name left
// the Running agents table empty on Windows while four agents were
// plainly running — which reads as a broken feature, not as a naming
// mismatch.
func TestRoots_MatchesWindowsExecutableNames(t *testing.T) {
	procs := []Proc{
		{PID: 1, Name: "claude.exe", RSSBytes: 100},
		{PID: 2, Name: "codex.EXE", RSSBytes: 200},
		{PID: 3, Name: "gemini", RSSBytes: 300},
		{PID: 4, Name: "chrome.exe", RSSBytes: 400},
	}

	got := Roots(procs, []string{"claude", "codex", "gemini"})

	if len(got) != 3 {
		t.Fatalf("matched %d roots, want 3: %+v", len(got), got)
	}
	for _, p := range got {
		if p.Name == "chrome.exe" {
			t.Fatal("matched a process that was not asked for")
		}
	}
}

// A suffix that merely looks like an extension must not be stripped, or
// a provider genuinely named "codex.exec" would be mistaken for "codex".
func TestBaseName_OnlyStripsARealExeSuffix(t *testing.T) {
	cases := map[string]string{
		"claude.exe":  "claude",
		"claude.EXE":  "claude",
		"claude":      "claude",
		"codex.exec":  "codex.exec",
		"node.exe.sh": "node.exe.sh",
		"a":           "a",
		"":            "",
	}
	for in, want := range cases {
		if got := BaseName(in); got != want {
			t.Errorf("BaseName(%q) = %q, want %q", in, got, want)
		}
	}
}
