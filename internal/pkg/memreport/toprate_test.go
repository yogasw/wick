package memreport

import (
	"testing"
	"time"
)

// The bug this whole file exists to prevent: ranking by RAW CPUTicks puts
// whatever has been alive longest at the top, not whatever is busy now. A
// browser open since yesterday would outrank the compiler pegging a core
// this second.
func TestRateTracker_RanksByRateNotLifetime(t *testing.T) {
	tr := NewRateTracker()
	t0 := time.Unix(1_700_000_000, 0)

	// oldTimer has burned a huge lifetime total but is now idle.
	// busyNow has barely any total but is working hard right now.
	first := []Proc{
		{PID: 1, Name: "oldTimer", CPUTicks: 500_000},
		{PID: 2, Name: "busyNow", CPUTicks: 10},
	}
	tr.Update(t0, first)

	second := []Proc{
		{PID: 1, Name: "oldTimer", CPUTicks: 500_000},  // unchanged: idle
		{PID: 2, Name: "busyNow", CPUTicks: 10 + 1000}, // +1000 ticks in 10s
	}
	rated := tr.Update(t0.Add(10*time.Second), second)

	top := TopRateBy(rated, RateByCPU, 5)
	if len(top) == 0 {
		t.Fatal("no processes ranked")
	}
	if top[0].Name != "busyNow" {
		t.Fatalf("top CPU = %q, want busyNow — ranking is using lifetime totals, not a rate", top[0].Name)
	}
	if top[0].CPUPct < 99 || top[0].CPUPct > 101 {
		t.Fatalf("CPUPct = %v, want ~100 (1000 ticks / 100Hz over 10s)", top[0].CPUPct)
	}
}

// The first observation has nothing to diff against. Reporting a rate
// derived from a process's whole lifetime would show absurd numbers for
// anything long-lived.
func TestRateTracker_FirstUpdateHasNoRates(t *testing.T) {
	tr := NewRateTracker()
	rated := tr.Update(time.Unix(1_700_000_000, 0),
		[]Proc{{PID: 1, Name: "x", CPUTicks: 999_999, IOReadBytes: 1 << 30}})

	if rated[0].CPUPct != 0 || rated[0].IOReadBps != 0 {
		t.Fatalf("first update invented rates: cpu=%v io=%v", rated[0].CPUPct, rated[0].IOReadBps)
	}
}

// A process that exits must not linger as a stale entry for the life of
// the server.
func TestRateTracker_DropsExitedProcesses(t *testing.T) {
	tr := NewRateTracker()
	t0 := time.Unix(1_700_000_000, 0)

	tr.Update(t0, []Proc{{PID: 1, Name: "a"}, {PID: 2, Name: "b"}})
	if len(tr.prev) != 2 {
		t.Fatalf("tracking %d processes, want 2", len(tr.prev))
	}

	tr.Update(t0.Add(time.Second), []Proc{{PID: 1, Name: "a"}})
	if len(tr.prev) != 1 {
		t.Fatalf("tracking %d processes after one exited, want 1", len(tr.prev))
	}
}

// A reused PID can make a counter go backwards; that must read as zero,
// not wrap to a nonsense rate near 2^64.
func TestRateTracker_CounterGoingBackwards(t *testing.T) {
	tr := NewRateTracker()
	t0 := time.Unix(1_700_000_000, 0)

	tr.Update(t0, []Proc{{PID: 1, CPUTicks: 50_000, IOReadBytes: 1 << 20}})
	rated := tr.Update(t0.Add(10*time.Second), []Proc{{PID: 1, CPUTicks: 5, IOReadBytes: 0}})

	if rated[0].CPUPct != 0 || rated[0].IOReadBps != 0 {
		t.Fatalf("backwards counters produced cpu=%v io=%v, want 0",
			rated[0].CPUPct, rated[0].IOReadBps)
	}
}

// A quiet machine must show a short list, not a table padded with idle
// system processes reading 0.
func TestTopRateBy_DropsZeroTail(t *testing.T) {
	rated := []ProcRate{
		{Proc: Proc{PID: 1, Name: "busy"}, CPUPct: 40},
		{Proc: Proc{PID: 2, Name: "idle1"}, CPUPct: 0},
		{Proc: Proc{PID: 3, Name: "idle2"}, CPUPct: 0},
	}
	got := TopRateBy(rated, RateByCPU, 10)
	if len(got) != 1 {
		t.Fatalf("got %d rows, want 1 — idle processes were not dropped", len(got))
	}
}

// Ties must not shuffle between refreshes; a table that reorders under
// the cursor is unreadable.
func TestTopRateBy_StableOnTies(t *testing.T) {
	rated := []ProcRate{
		{Proc: Proc{PID: 30, Name: "c"}, CPUPct: 5},
		{Proc: Proc{PID: 10, Name: "a"}, CPUPct: 5},
		{Proc: Proc{PID: 20, Name: "b"}, CPUPct: 5},
	}
	got := TopRateBy(rated, RateByCPU, 10)
	if got[0].PID != 10 || got[1].PID != 20 || got[2].PID != 30 {
		t.Fatalf("ties did not break on PID: %v %v %v", got[0].PID, got[1].PID, got[2].PID)
	}
}

// Memory ranking needs no diff — RSS is a level, not a counter — so it
// must work on the very first snapshot.
func TestTopRateBy_MemoryWorksImmediately(t *testing.T) {
	tr := NewRateTracker()
	rated := tr.Update(time.Now(), []Proc{
		{PID: 1, Name: "small", RSSBytes: 100},
		{PID: 2, Name: "big", RSSBytes: 900},
	})
	got := TopRateBy(rated, RateByMem, 5)
	if len(got) == 0 || got[0].Name != "big" {
		t.Fatalf("memory ranking failed on the first snapshot: %+v", got)
	}
}

// TopBy (the non-rate variant) still ranks levels correctly.
func TestTopBy_RanksAndDropsZeros(t *testing.T) {
	procs := []Proc{
		{PID: 1, Name: "a", RSSBytes: 10},
		{PID: 2, Name: "b", RSSBytes: 900},
		{PID: 3, Name: "zero", RSSBytes: 0},
	}
	got := TopBy(procs, ByRSS, 10)
	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2 (the zero row dropped)", len(got))
	}
	if got[0].Name != "b" {
		t.Fatalf("top = %q, want b", got[0].Name)
	}
}
