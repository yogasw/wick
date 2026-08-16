package memreport

import (
	"testing"
	"time"
)

func tree(pid int, name string, rss, ticks, ioR, ioW uint64) []Proc {
	return []Proc{{
		PID: pid, PPID: 1, Name: name,
		RSSBytes: rss, CPUTicks: ticks, IOReadBytes: ioR, IOWriteBytes: ioW,
	}}
}

// Cumulative counters must become rates. /proc reports totals since
// process start, so reporting them raw would show a long-lived agent at
// an ever-climbing "CPU%" that never falls.
func TestHistory_TurnsCountersIntoRates(t *testing.T) {
	h := NewHistory(time.Hour, 100)
	t0 := time.Unix(1_700_000_000, 0)

	// First sample has no predecessor: rates must read 0, not a number
	// derived from the process's whole lifetime.
	h.Record(t0, tree(100, "claude", 1000, 500, 4096, 8192), tree(100, "claude", 1000, 500, 4096, 8192), 3<<30, 1<<30)
	first := h.Agents(time.Time{})
	if len(first) != 1 {
		t.Fatalf("got %d samples, want 1", len(first))
	}
	if first[0].CPUPct != 0 || first[0].IOReadBps != 0 {
		t.Fatalf("first sample invented rates: cpu=%v ioread=%v", first[0].CPUPct, first[0].IOReadBps)
	}

	// 10s later, +1000 ticks = 10 CPU-seconds = 100% of one core.
	t1 := t0.Add(10 * time.Second)
	p := tree(100, "claude", 2000, 1500, 4096+10240, 8192+5120)
	h.Record(t1, p, p, 3<<30, 1<<30)

	got := h.Agents(time.Time{})
	last := got[len(got)-1]
	if last.CPUPct < 99 || last.CPUPct > 101 {
		t.Fatalf("CPUPct = %v, want ~100 (1000 ticks / 100Hz over 10s)", last.CPUPct)
	}
	if last.IOReadBps != 1024 {
		t.Fatalf("IOReadBps = %d, want 1024 (10240 bytes over 10s)", last.IOReadBps)
	}
}

// Retention is the point of the whole buffer: a machine left running for
// a month must not report a month of history.
func TestHistory_PurgesByRetention(t *testing.T) {
	h := NewHistory(30*time.Minute, 10_000)
	base := time.Unix(1_700_000_000, 0)

	// One sample per minute for two hours — well past the 30m window.
	for i := 0; i < 120; i++ {
		at := base.Add(time.Duration(i) * time.Minute)
		p := tree(100, "claude", 1000, uint64(i*100), 0, 0)
		h.Record(at, p, p, 3<<30, 1<<30)
	}

	st := h.Stats()
	if st.MachinePoints > 31 {
		t.Fatalf("retained %d machine points, want <=31 for a 30m window at 1/min", st.MachinePoints)
	}
	if st.SpanSec > 31*60 {
		t.Fatalf("span = %ds, want <= 1860s (30m)", st.SpanSec)
	}
	// And what survived must be the RECENT end, not the oldest.
	m := h.Machine(time.Time{})
	newest := m[len(m)-1].At
	if !newest.Equal(base.Add(119 * time.Minute)) {
		t.Fatalf("newest sample = %v, want the last one recorded", newest)
	}
}

// The count ceiling is the backstop: a misconfigured 1-second interval
// must not grow the buffer without limit just because the time window
// technically allows it.
func TestHistory_PurgesByMaxPoints(t *testing.T) {
	h := NewHistory(24*time.Hour, 50)
	base := time.Unix(1_700_000_000, 0)

	for i := 0; i < 500; i++ {
		at := base.Add(time.Duration(i) * time.Second)
		p := tree(100, "claude", 1000, 0, 0, 0)
		h.Record(at, p, p, 3<<30, 1<<30)
	}

	st := h.Stats()
	if st.MachinePoints > 50 || st.AgentPoints > 50 {
		t.Fatalf("points = %d/%d, want <=50 despite a 24h window",
			st.AgentPoints, st.MachinePoints)
	}
}

// Lowering retention in the UI should free memory immediately, not at
// some unpredictable later sample.
func TestHistory_SetRetentionPurgesNow(t *testing.T) {
	h := NewHistory(2*time.Hour, 10_000)
	now := time.Now()
	for i := 0; i < 60; i++ {
		at := now.Add(-time.Duration(59-i) * time.Minute)
		p := tree(100, "claude", 1000, 0, 0, 0)
		h.Record(at, p, p, 3<<30, 1<<30)
	}
	if before := h.Stats().MachinePoints; before < 50 {
		t.Fatalf("setup recorded only %d points", before)
	}

	h.SetRetention(10 * time.Minute)

	if after := h.Stats().MachinePoints; after > 11 {
		t.Fatalf("after lowering retention to 10m, %d points remain", after)
	}
}

// An agent that exits must not leave its rate-tracking entry behind, or a
// long-running wick accumulates one per agent it ever spawned.
func TestHistory_DropsCountersForExitedAgents(t *testing.T) {
	h := NewHistory(time.Hour, 100)
	t0 := time.Unix(1_700_000_000, 0)

	both := []Proc{
		{PID: 100, PPID: 1, Name: "claude", RSSBytes: 1000},
		{PID: 200, PPID: 1, Name: "codex", RSSBytes: 2000},
	}
	h.Record(t0, both, both, 3<<30, 1<<30)
	if len(h.prev) != 2 {
		t.Fatalf("tracking %d pids, want 2", len(h.prev))
	}

	only := []Proc{{PID: 100, PPID: 1, Name: "claude", RSSBytes: 1000}}
	h.Record(t0.Add(time.Minute), only, only, 3<<30, 1<<30)

	if len(h.prev) != 1 {
		t.Fatalf("tracking %d pids after one exited, want 1", len(h.prev))
	}
	if _, stale := h.prev[200]; stale {
		t.Fatal("exited agent's counters were not dropped")
	}
}

// A reused PID can make a counter appear to go backwards. That must read
// as zero, not wrap to a nonsense rate near 2^64.
func TestHistory_CounterGoingBackwardsIsZero(t *testing.T) {
	h := NewHistory(time.Hour, 100)
	t0 := time.Unix(1_700_000_000, 0)

	big := tree(100, "claude", 1000, 50_000, 1<<20, 1<<20)
	h.Record(t0, big, big, 3<<30, 1<<30)

	small := tree(100, "claude", 1000, 10, 0, 0)
	h.Record(t0.Add(10*time.Second), small, small, 3<<30, 1<<30)

	got := h.Agents(time.Time{})
	last := got[len(got)-1]
	if last.CPUPct != 0 || last.IOReadBps != 0 || last.IOWriteBps != 0 {
		t.Fatalf("counters going backwards produced rates cpu=%v r=%v w=%v",
			last.CPUPct, last.IOReadBps, last.IOWriteBps)
	}
}

// Peak RSS and peak CPU can happen at different moments; keeping one
// whole "worst sample" would hide whichever spike came second.
func TestHistory_PeaksTracksEachDimension(t *testing.T) {
	h := NewHistory(time.Hour, 100)
	t0 := time.Unix(1_700_000_000, 0)

	// Sample 1 establishes a baseline (no rates yet).
	p := tree(100, "claude", 1000, 0, 0, 0)
	h.Record(t0, p, p, 3<<30, 1<<30)
	// Sample 2: CPU spike, low memory.
	p = tree(100, "claude", 1000, 1000, 0, 0)
	h.Record(t0.Add(10*time.Second), p, p, 3<<30, 1<<30)
	// Sample 3: memory spike, no further CPU.
	p = tree(100, "claude", 9_000_000, 1000, 0, 0)
	h.Record(t0.Add(20*time.Second), p, p, 3<<30, 1<<30)

	peak := h.Peaks()["claude"]
	if peak.RSSBytes != 9_000_000 {
		t.Fatalf("peak RSS = %d, want 9000000", peak.RSSBytes)
	}
	if peak.CPUPct < 99 {
		t.Fatalf("peak CPU = %v, want the earlier ~100 spike retained", peak.CPUPct)
	}
}

// A zero or negative retention must not mean "keep everything" — that
// would be an unbounded buffer inside a feature whose job is preventing
// memory exhaustion.
func TestNewHistory_RejectsUnboundedConfig(t *testing.T) {
	h := NewHistory(0, 0)
	st := h.Stats()
	if st.RetentionSec <= 0 || st.MaxPoints <= 0 {
		t.Fatalf("zero config produced unbounded buffer: %+v", st)
	}
}
