package memreport

import (
	"sort"
	"sync"
	"time"
)

// history.go keeps a bounded, in-memory time series of resource usage so
// the dashboard can show a trend rather than a single instant.
//
// In memory, not on disk, and deliberately: this is diagnostic telemetry
// whose whole value is recency. Persisting it would add a schema, a
// migration, and disk growth on the exact machines that are already short
// of resources — the ones this feature exists to protect.
//
// Two independent bounds, because either alone fails:
//
//   - Retention (time): drop anything older than the window, so a machine
//     left running for a month does not report a month of history.
//   - MaxPoints (count): a hard ceiling on the ring, so a misconfigured
//     short interval cannot grow the slice without limit no matter what
//     the retention window says.

// Sample is one observation of one agent's process tree.
type Sample struct {
	At         time.Time `json:"at"`
	Provider   string    `json:"provider"`
	PID        int       `json:"pid"`
	RSSBytes   uint64    `json:"rss_bytes"`
	CPUPct     float64   `json:"cpu_pct"`
	IOReadBps  uint64    `json:"io_read_bps"`
	IOWriteBps uint64    `json:"io_write_bps"`
	Procs      int       `json:"procs"`
}

// MachineSample is one observation of the machine as a whole, recorded
// alongside the per-agent rows so a spike can be read in context: 1.5 GB
// of agents means something different on a 3 GB box than on a 32 GB one.
type MachineSample struct {
	At             time.Time `json:"at"`
	TotalBytes     uint64    `json:"total_bytes"`
	AvailableBytes uint64    `json:"available_bytes"`
	AgentBytes     uint64    `json:"agent_bytes"`
	AgentCPUPct    float64   `json:"agent_cpu_pct"`
	AgentProcs     int       `json:"agent_procs"`

	// Machine-wide totals, recorded alongside the agent figures so the
	// chart has something to show when no agent is running — which is
	// most of the time on a box that is merely slow. Also gives the agent
	// numbers a denominator: 400 MB of agents means one thing when the
	// machine is at 2 GB and another when it is at 30.
	MachineUsedBytes uint64  `json:"machine_used_bytes"`
	MachineCPUPct    float64 `json:"machine_cpu_pct"`
	MachineProcs     int     `json:"machine_procs"`
}

// prevCounters holds the cumulative readings from the previous sample so
// the next one can turn them into rates.
type prevCounters struct {
	at       time.Time
	cpuTicks uint64
	ioRead   uint64
	ioWrite  uint64
}

// History is a bounded time series, safe for concurrent use.
type History struct {
	mu sync.Mutex

	retention time.Duration
	maxPoints int

	agents  []Sample
	machine []MachineSample

	// prev is keyed by PID so rates survive across samples. Entries for
	// PIDs that stop appearing are dropped on each sample, so an agent
	// that exits does not leak an entry for the process's lifetime.
	prev map[int]prevCounters

	// machineProcs holds the previous CPU tick count for EVERY process, so
	// the machine-wide CPU figure is a rate rather than a lifetime total.
	// Separate from prev, which is keyed by agent root and holds subtree
	// sums rather than per-process counters.
	machineProcs map[int]uint64
	machineAt    time.Time
}

// NewHistory builds a series bounded by both a time window and a hard
// point ceiling. Non-positive values fall back to defaults rather than
// meaning "unlimited": an unbounded diagnostic buffer is a memory leak in
// a feature whose purpose is preventing memory exhaustion.
func NewHistory(retention time.Duration, maxPoints int) *History {
	if retention <= 0 {
		retention = 6 * time.Hour
	}
	if maxPoints <= 0 {
		maxPoints = 4096
	}
	return &History{
		retention:    retention,
		maxPoints:    maxPoints,
		prev:         map[int]prevCounters{},
		machineProcs: map[int]uint64{},
	}
}

// SetRetention updates the window at runtime and purges immediately, so
// lowering it in the UI frees memory now rather than at the next sample.
func (h *History) SetRetention(d time.Duration) {
	if d <= 0 {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.retention = d
	h.purgeLocked(time.Now())
}

// Record turns one /proc snapshot into rate-bearing samples.
//
// now is passed in rather than read from the clock so tests can drive the
// rate arithmetic deterministically.
func (h *History) Record(now time.Time, procs []Proc, roots []Proc, totalBytes, availBytes uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	seen := make(map[int]bool, len(roots))
	// Agent totals — named for what they are. An earlier version called
	// these "machine*", which is exactly the conflation this file now has
	// to keep straight: agents are a subset of the machine, not all of it.
	var agentBytes uint64
	var agentCPU float64
	var agentProcs int

	for _, r := range roots {
		seen[r.PID] = true
		t := SumSubtreeAll(procs, r.PID)

		s := Sample{
			At:       now,
			Provider: r.Name,
			PID:      r.PID,
			RSSBytes: t.RSSBytes,
			Procs:    t.Procs,
		}
		// Rates need a previous reading. The first sample of a process
		// reports 0 rather than a fabricated number derived from its
		// lifetime total, which would spike wildly for a long-lived agent.
		if p, ok := h.prev[r.PID]; ok {
			elapsed := now.Sub(p.at).Seconds()
			if elapsed > 0 {
				s.CPUPct = CPUPercent(saturatingSub(t.CPUTicks, p.cpuTicks), elapsed)
				s.IOReadBps = uint64(float64(saturatingSub(t.IOReadBytes, p.ioRead)) / elapsed)
				s.IOWriteBps = uint64(float64(saturatingSub(t.IOWriteBytes, p.ioWrite)) / elapsed)
			}
		}
		h.prev[r.PID] = prevCounters{
			at: now, cpuTicks: t.CPUTicks, ioRead: t.IOReadBytes, ioWrite: t.IOWriteBytes,
		}

		h.agents = append(h.agents, s)
		agentBytes += t.RSSBytes
		agentCPU += s.CPUPct
		agentProcs += t.Procs
	}

	// Machine-wide totals, summed across EVERY process — not just agents.
	// Without these the chart is blank whenever no agent is running, which
	// is precisely when someone is asking why the box is slow.
	var machineUsed uint64
	var machineCPU float64
	for _, p := range procs {
		machineUsed += p.RSSBytes
		if prev, ok := h.machineProcs[p.PID]; ok {
			if elapsed := now.Sub(h.machineAt).Seconds(); elapsed > 0 {
				machineCPU += CPUPercent(saturatingSub(p.CPUTicks, prev), elapsed)
			}
		}
	}
	// Replace wholesale: exited processes must not linger as stale
	// entries for the life of the server.
	nextMachine := make(map[int]uint64, len(procs))
	for _, p := range procs {
		nextMachine[p.PID] = p.CPUTicks
	}
	h.machineProcs = nextMachine
	h.machineAt = now

	// Drop counters for PIDs that are gone, so a long-running wick does
	// not accumulate one entry per agent it ever spawned.
	for pid := range h.prev {
		if !seen[pid] {
			delete(h.prev, pid)
		}
	}

	h.machine = append(h.machine, MachineSample{
		At:             now,
		TotalBytes:     totalBytes,
		AvailableBytes: availBytes,
		AgentBytes:     agentBytes,
		AgentCPUPct:    agentCPU,
		AgentProcs:     agentProcs,

		MachineUsedBytes: machineUsed,
		MachineCPUPct:    machineCPU,
		MachineProcs:     len(procs),
	})

	h.purgeLocked(now)
}

// purgeLocked enforces both bounds. Caller MUST hold h.mu.
func (h *History) purgeLocked(now time.Time) {
	cutoff := now.Add(-h.retention)

	// Samples are appended in time order, so the first index at or after
	// the cutoff bounds everything worth keeping.
	i := sort.Search(len(h.agents), func(i int) bool { return !h.agents[i].At.Before(cutoff) })
	h.agents = append([]Sample(nil), h.agents[i:]...)

	j := sort.Search(len(h.machine), func(i int) bool { return !h.machine[i].At.Before(cutoff) })
	h.machine = append([]MachineSample(nil), h.machine[j:]...)

	// The count ceiling is the backstop for a misconfigured interval that
	// packs more points into the window than the window ever anticipated.
	if len(h.agents) > h.maxPoints {
		h.agents = append([]Sample(nil), h.agents[len(h.agents)-h.maxPoints:]...)
	}
	if len(h.machine) > h.maxPoints {
		h.machine = append([]MachineSample(nil), h.machine[len(h.machine)-h.maxPoints:]...)
	}
}

// Agents returns the per-agent series, oldest first. Optionally limited
// to samples at or after since.
func (h *History) Agents(since time.Time) []Sample {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]Sample, 0, len(h.agents))
	for _, s := range h.agents {
		if s.At.Before(since) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// Machine returns the machine-wide series, oldest first.
func (h *History) Machine(since time.Time) []MachineSample {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]MachineSample, 0, len(h.machine))
	for _, s := range h.machine {
		if s.At.Before(since) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// Peaks reports the highest RSS and CPU seen per provider across the
// retained window — the numbers a limit must actually accommodate, and
// exactly what a single snapshot misses.
func (h *History) Peaks() map[string]Sample {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string]Sample{}
	for _, s := range h.agents {
		cur, ok := out[s.Provider]
		if !ok {
			out[s.Provider] = s
			continue
		}
		// Peak RSS and peak CPU can occur at different moments; keep the
		// highest of each rather than one whole sample, or a CPU spike
		// would be hidden by a later memory-heavy one.
		if s.RSSBytes > cur.RSSBytes {
			cur.RSSBytes = s.RSSBytes
			cur.At = s.At
		}
		if s.CPUPct > cur.CPUPct {
			cur.CPUPct = s.CPUPct
		}
		if s.Procs > cur.Procs {
			cur.Procs = s.Procs
		}
		out[s.Provider] = cur
	}
	return out
}

// Stats describes what the buffer currently holds, so the UI can show
// "12 minutes of history, 240 points" instead of an unlabelled chart.
type Stats struct {
	AgentPoints   int        `json:"agent_points"`
	MachinePoints int        `json:"machine_points"`
	RetentionSec  int        `json:"retention_sec"`
	MaxPoints     int        `json:"max_points"`
	OldestAt      *time.Time `json:"oldest_at,omitempty"`
	SpanSec       int        `json:"span_sec"`
}

// Stats reports the buffer's current contents and bounds.
func (h *History) Stats() Stats {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := Stats{
		AgentPoints:   len(h.agents),
		MachinePoints: len(h.machine),
		RetentionSec:  int(h.retention.Seconds()),
		MaxPoints:     h.maxPoints,
	}
	if len(h.machine) > 0 {
		oldest := h.machine[0].At
		st.OldestAt = &oldest
		st.SpanSec = int(h.machine[len(h.machine)-1].At.Sub(oldest).Seconds())
	}
	return st
}

// saturatingSub avoids a wrapped result when a counter appears to go
// backwards — which happens when a PID is reused, or when a subtree loses
// a heavy child between samples.
func saturatingSub(now, prev uint64) uint64 {
	if now < prev {
		return 0
	}
	return now - prev
}
