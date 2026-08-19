package agents

import (
	"net/http"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider/memscope"
	"github.com/yogasw/wick/internal/agents/provider/memscope/wrapper"
	"github.com/yogasw/wick/internal/appname"
	"github.com/yogasw/wick/internal/pkg/memreport"
	"github.com/yogasw/wick/internal/pkg/sysmem"
	"github.com/yogasw/wick/pkg/tool"
)

// memory_handler.go reports what agents actually use, so a limit is
// chosen from measurement rather than guessed.
//
// It answers honestly when it cannot help: a machine without systemd
// scopes gets a notice saying so, because an operator reading a
// normal-looking dashboard would otherwise believe they are protected
// when they are not.

// memoryAgentRow is one agent process tree in the report.
type memoryAgentRow struct {
	Name string `json:"name"`
	PID  int    `json:"pid"`
	// TreeBytes is the whole subtree, which is where the memory actually
	// is — an agent's own RSS omits the browser it started.
	TreeBytes uint64 `json:"tree_bytes"`
	// Largest names the heaviest descendant. The total alone invites
	// raising a limit; naming chromium points at the real cause.
	LargestName  string `json:"largest_name,omitempty"`
	LargestBytes uint64 `json:"largest_bytes,omitempty"`
	// Procs counts the subtree. A runaway that spawns thousands of tiny
	// processes shows up here while TreeBytes still looks healthy.
	Procs int `json:"procs"`
	// CPUPct and the IO rates come from the history buffer, which is the
	// only place two samples exist to derive a rate from. Zero when
	// history is off or this agent has only been seen once.
	CPUPct     float64 `json:"cpu_pct"`
	IOReadBps  uint64  `json:"io_read_bps"`
	IOWriteBps uint64  `json:"io_write_bps"`
	// Isolated reports whether a memory ceiling actually applies to this
	// agent right now — read from its cgroup, not inferred from the
	// configured mode. The two disagree more often than they should: a
	// guard set to enforce still leaves an agent uncovered when the
	// mechanism is missing, and a row that looks identical either way is
	// how an operator ends up believing they are protected.
	Isolated bool `json:"isolated"`
	// Peak* are the highest values seen across the retained window — the
	// numbers a limit must accommodate, which a single instant misses.
	PeakBytes  uint64  `json:"peak_bytes,omitempty"`
	PeakCPUPct float64 `json:"peak_cpu_pct,omitempty"`
	// Processes is the task-manager view of this tree, heaviest first and
	// capped. The aggregate above says how much the agent uses; this says
	// which process to look at.
	Processes []processRow `json:"processes,omitempty"`
}

// processRow is one process inside an agent's tree.
type processRow struct {
	PID      int    `json:"pid"`
	PPID     int    `json:"ppid"`
	Name     string `json:"name"`
	RSSBytes uint64 `json:"rss_bytes"`
}

// maxProcessRows caps the per-agent process list. A browser-driving agent
// can hold dozens of renderers; past the top handful they are noise, and
// an uncapped list grows the payload without informing the operator.
const maxProcessRows = 12

// diskRow reports capacity where wick writes.
//
// Distinct from the IO rates above, and the distinction matters: a busy
// disk makes everything slow, a FULL disk makes writes fail outright.
// Wick writes continuously (session transcripts, spawn logs, trace
// events), so an operator needs the second number before the writes start
// failing.
type diskRow struct {
	Path       string  `json:"path"`
	TotalBytes uint64  `json:"total_bytes"`
	FreeBytes  uint64  `json:"free_bytes"`
	AvailBytes uint64  `json:"avail_bytes"`
	UsedBytes  uint64  `json:"used_bytes"`
	UsedPct    float64 `json:"used_pct"`
	// Pressure is "ok" / "warn" / "full", graded on percentage AND
	// absolute free space together. Decided here rather than in the UI so
	// the CLI and the page cannot disagree about what counts as alarming.
	Pressure string `json:"pressure"`
	Known    bool   `json:"known"`
}

// memoryReport is the payload behind GET /agents/memory.
type memoryReport struct {
	ScopesAvailable bool   `json:"scopes_available"`
	Notice          string `json:"notice,omitempty"`
	Mode            string `json:"mode"`
	Method          string `json:"method"`

	TotalBytes     uint64 `json:"total_bytes,omitempty"`
	AvailableBytes uint64 `json:"available_bytes,omitempty"`
	MachineKnown   bool   `json:"machine_known"`
	// CPUCores is the ceiling for every CPU percentage on this page: the
	// figures are percent of ONE core, so a busy machine legitimately
	// reads past 100% and looks like a bug without this.
	CPUCores int `json:"cpu_cores"`

	Agents []memoryAgentRow `json:"agents"`
	// ProcessesReadable is false where /proc does not exist, so the empty
	// Agents list is not mistaken for "nothing is running".
	ProcessesReadable bool `json:"processes_readable"`

	Suggested agentconfig.MemoryDefaults `json:"suggested"`
	Current   memoryCurrentLimits        `json:"current"`

	// History describes the sample buffer so the UI can label its chart
	// ("12 minutes, 48 points") instead of drawing an unbounded axis.
	History memreport.Stats `json:"history"`

	// Disk is capacity where the data tree lives — a different failure
	// from the IO rates per agent.
	Disk diskRow `json:"disk"`

	// Top is the machine-wide process view. Deliberately not scoped to
	// wick: when the box is slow, the cause is often not an agent, and a
	// dashboard that can only see its own processes cannot say so.
	Top topProcesses `json:"top"`
}

// memoryCurrentLimits echoes what is configured now, so the UI can show
// suggested beside actual instead of making the operator cross-reference.
type memoryCurrentLimits struct {
	AgentMemoryMaxMB    int `json:"agent_memory_max_mb"`
	AgentsTotalMemoryMB int `json:"agents_total_memory_mb"`
	ToolMemoryMaxMB     int `json:"tool_memory_max_mb"`
	MinFreeMemoryMB     int `json:"min_free_memory_mb"`
}

// agentProcessNames are the process names reported as tree roots.
var agentProcessNames = []string{"claude", "codex", "gemini"}

// resourceHistory is the process-wide sample buffer, installed at boot by
// SetResourceHistory. nil = history was never started (a test binary, or
// an operator who turned it off), and every reader below degrades to the
// live snapshot rather than failing.
var resourceHistory *memreport.History

// SetResourceHistory installs the buffer the API reads from. Called once
// from server startup, alongside the sampler that fills it.
func SetResourceHistory(h *memreport.History) { resourceHistory = h }

// topRates diffs consecutive machine-wide snapshots so the top-process
// table can rank by RATE rather than lifetime totals — otherwise the
// busiest process loses to whatever has merely been running longest.
//
// Package-level with a mutex because it must persist between requests: a
// rate needs two observations, and each request supplies only one.
var (
	topRatesMu sync.Mutex
	topRates   = memreport.NewRateTracker()
)

// maxTopRows caps each top table. Five is a summary; the explorer below
// it is where an operator goes for the full list.
const maxTopRows = 5

// topProcessRow is one row of the machine-wide "what is using this box"
// tables. Unlike the per-agent list, these are not scoped to wick at all:
// the answer to "why is this machine slow" is frequently not an agent.
type topProcessRow struct {
	PID  int    `json:"pid"`
	Name string `json:"name"`
	// Cmdline identifies WHICH process this is when the name cannot:
	// "node", "python3", and "MainThread" are all ambiguous on a busy
	// machine. Empty for kernel threads and processes another user owns.
	Cmdline    string  `json:"cmdline,omitempty"`
	RSSBytes   uint64  `json:"rss_bytes"`
	CPUPct     float64 `json:"cpu_pct"`
	IOReadBps  uint64  `json:"io_read_bps"`
	IOWriteBps uint64  `json:"io_write_bps"`
	// Isolated reports whether a memory ceiling actually applies to this
	// agent right now — read from its cgroup, not inferred from the
	// configured mode. The two disagree more often than they should: a
	// guard set to enforce still leaves an agent uncovered when the
	// mechanism is missing, and a row that looks identical either way is
	// how an operator ends up believing they are protected.
	Isolated bool `json:"isolated"`
	// Count is how many processes share this name. >1 means the row is a
	// group; the summary tables always group, so "chrome.exe × 26" is one
	// row rather than 26 competing for the top five.
	Count int `json:"count"`
}

// topProcesses is the machine-wide view, ranked three ways because the
// three questions are different: what is holding memory, what is burning
// CPU, and what is hammering the disk are usually different processes.
//
// Grouped by executable, like the explorer below it. Ungrouped, a browser
// with 26 processes fills every slot with its own renderers and pushes out
// everything else — and "chrome.exe 662 MB" is the wrong answer to "what
// is using this machine" when the real figure is 3.9 GB across 26.
type topProcesses struct {
	Available bool            `json:"available"`
	Total     int             `json:"total"`
	ByMemory  []topProcessRow `json:"by_memory"`
	ByCPU     []topProcessRow `json:"by_cpu"`
	ByIO      []topProcessRow `json:"by_io"`
}

func toTopRows(in []memreport.ProcRate) []topProcessRow {
	out := make([]topProcessRow, 0, len(in))
	for _, p := range in {
		out = append(out, topProcessRow{
			PID: p.PID, Name: p.Name, Cmdline: p.Cmdline, RSSBytes: p.RSSBytes,
			CPUPct: p.CPUPct, IOReadBps: p.IOReadBps, IOWriteBps: p.IOWriteBps,
		})
	}
	return out
}

// buildTopProcesses samples the machine and ranks it, grouped by
// executable so one browser does not occupy every slot with its own
// renderers.
//
// CPU and IO read zero on the very first call after startup, because a
// rate needs a predecessor. That is correct rather than unfortunate:
// inventing a rate from a process's lifetime total would rank a
// day-old browser above a compiler pegging a core right now.
func buildTopProcesses(procs []memreport.Proc) topProcesses {
	topRatesMu.Lock()
	defer topRatesMu.Unlock()

	rated := topRates.Update(time.Now(), procs)
	// Machine memory is not needed here — the summary shows absolute
	// figures, and the share column lives in the explorer below.
	groups := memreport.GroupBy(rated, 0)

	return topProcesses{
		Available: true,
		Total:     len(procs),
		ByMemory:  toTopGroupRows(memreport.TopGroupsBy(groups, memreport.GroupByMem, maxTopRows)),
		ByCPU:     toTopGroupRows(memreport.TopGroupsBy(groups, memreport.GroupByCPU, maxTopRows)),
		ByIO:      toTopGroupRows(memreport.TopGroupsBy(groups, memreport.GroupByIO, maxTopRows)),
	}
}

// toTopGroupRows flattens groups into summary rows. PID is left at zero:
// a group has no single pid, and the explorer below is where an operator
// goes to find the specific one.
func toTopGroupRows(in []memreport.ProcGroup) []topProcessRow {
	out := make([]topProcessRow, 0, len(in))
	for _, g := range in {
		out = append(out, topProcessRow{
			Name:       g.Name,
			Count:      g.Count,
			RSSBytes:   g.RSSBytes,
			CPUPct:     g.CPUPct,
			IOReadBps:  g.IOReadBps,
			IOWriteBps: g.IOWriteBps,
		})
	}
	return out
}

// memoryReportHandler serves the diagnostics payload.
func memoryReportHandler(c *tool.Ctx) {
	if !requireAdmin(c) {
		return
	}
	c.JSON(http.StatusOK, buildMemoryReport())
}

// buildMemoryReport assembles the payload. Split from the handler so the
// assembly is testable without an HTTP request or an admin session.
func buildMemoryReport() memoryReport {
	rep := memoryReport{
		ScopesAvailable: memscope.Available(),
		Mode:            memGuardConfig("memory_guard_mode", agentconfig.MemGuardOff),
		Method:          memGuardConfig("memory_guard_method", agentconfig.MethodAuto),
		Current: memoryCurrentLimits{
			AgentMemoryMaxMB:    memGuardInt("agent_memory_max_mb"),
			AgentsTotalMemoryMB: memGuardInt("agents_total_memory_mb"),
			ToolMemoryMaxMB:     memGuardInt("tool_memory_max_mb"),
			MinFreeMemoryMB:     memGuardInt("min_free_memory_mb"),
		},
	}
	if !rep.ScopesAvailable {
		// Say it plainly. Degrading silently is how an operator ends up
		// believing they are guarded when nothing is enforcing anything.
		// Names neither mechanism specifically: this branch is reached only
		// when BOTH the systemd user session and raw cgroupfs failed, so
		// blaming systemd alone would send an operator on a Fly.io-style
		// host (no systemd, but a usable cgroup v1 mount) chasing the
		// wrong thing.
		rep.Notice = "scope isolation unavailable — no systemd user session and no writable cgroup filesystem; agents run unguarded"
	}

	total, okT := sysmem.Total()
	avail, _ := sysmem.Available()
	rep.TotalBytes, rep.AvailableBytes, rep.MachineKnown = total, avail, okT
	rep.CPUCores = runtime.NumCPU()

	// Capacity where the data tree actually lives — AgentsDir follows
	// WICK_DATA_DIR, so a relocated tree reports its own filesystem rather
	// than whichever one holds the binary.
	if du, ok := sysmem.Disk(appname.AgentsDir()); ok {
		rep.Disk = diskRow{
			Path:       du.Path,
			TotalBytes: du.TotalBytes,
			FreeBytes:  du.FreeBytes,
			AvailBytes: du.AvailBytes,
			UsedBytes:  du.UsedBytes(),
			UsedPct:    du.UsedPct(),
			Pressure:   du.Pressure(),
			Known:      true,
		}
	} else {
		rep.Disk = diskRow{Path: appname.AgentsDir()}
	}

	// Rates and peaks live only in the history buffer — a rate needs two
	// samples, and a peak needs the whole window. The live snapshot below
	// supplies the instantaneous sizes either way, so the page still works
	// with history switched off; it just shows no CPU column.
	var latest map[int]memreport.Sample
	var peaks map[string]memreport.Sample
	if resourceHistory != nil {
		rep.History = resourceHistory.Stats()
		peaks = resourceHistory.Peaks()
		latest = map[int]memreport.Sample{}
		for _, s := range resourceHistory.Agents(time.Time{}) {
			latest[s.PID] = s // series is time-ordered, so the last write wins
		}
	}

	procs, err := memreport.Snapshot()
	rep.ProcessesReadable = err == nil
	if err == nil {
		// Coverage per agent, from the same scan the Coverage panel uses,
		// so the two cannot disagree about the same process.
		isolatedPIDs := map[int]bool{}
		if states, err := wrapper.Scan(wrapper.Providers, os.Getpid(), memscope.SliceName); err == nil {
			for _, st := range states {
				isolatedPIDs[st.PID] = st.Isolated
			}
		}
		rep.Top = buildTopProcesses(procs)
		for _, r := range memreport.Roots(procs, agentProcessNames) {
			t := memreport.SumSubtreeAll(procs, r.PID)
			row := memoryAgentRow{
				Name:      r.Name,
				PID:       r.PID,
				TreeBytes: t.RSSBytes,
				Procs:     t.Procs,
			}
			if big := memreport.LargestDescendant(procs, r.PID); big.PID != 0 {
				row.LargestName, row.LargestBytes = big.Name, big.RSSBytes
			}
			if s, ok := latest[r.PID]; ok {
				row.CPUPct, row.IOReadBps, row.IOWriteBps = s.CPUPct, s.IOReadBps, s.IOWriteBps
			}
			if p, ok := peaks[r.Name]; ok {
				row.PeakBytes, row.PeakCPUPct = p.RSSBytes, p.CPUPct
			}
			row.Isolated = isolatedPIDs[r.PID]
			for _, sp := range memreport.Subtree(procs, r.PID, maxProcessRows) {
				row.Processes = append(row.Processes, processRow{
					PID: sp.PID, PPID: sp.PPID, Name: sp.Name, RSSBytes: sp.RSSBytes,
				})
			}
			rep.Agents = append(rep.Agents, row)
		}
	}

	if okT {
		maxConc := memGuardInt("max_concurrent")
		if maxConc < 1 {
			maxConc = 1
		}
		rep.Suggested = agentconfig.DeriveMemoryDefaults(total, maxConc)
	}
	return rep
}

// memorySeries is the payload behind GET /agents/api/memory/series.
type memorySeries struct {
	Enabled bool                      `json:"enabled"`
	Stats   memreport.Stats           `json:"stats"`
	Machine []memreport.MachineSample `json:"machine"`
	Agents  []memreport.Sample        `json:"agents"`
}

// memorySeriesHandler serves the recorded time series for the charts.
//
// ?minutes=N narrows the window. Callers asking for more than is retained
// simply get everything retained — the buffer, not the query, is the
// authority on how far back the data goes.
func memorySeriesHandler(c *tool.Ctx) {
	if !requireAdmin(c) {
		return
	}
	if resourceHistory == nil {
		// Not an error: history off is a supported configuration, and the
		// page renders a live-snapshot-only view from this flag.
		c.JSON(http.StatusOK, memorySeries{Enabled: false})
		return
	}
	since := time.Time{}
	if n, err := strconv.Atoi(c.Query("minutes")); err == nil && n > 0 {
		since = time.Now().Add(-time.Duration(n) * time.Minute)
	}
	c.JSON(http.StatusOK, memorySeries{
		Enabled: true,
		Stats:   resourceHistory.Stats(),
		Machine: resourceHistory.Machine(since),
		Agents:  resourceHistory.Agents(since),
	})
}

// applySuggestedMemoryHandler writes the derived limits into config.
//
// It deliberately does NOT touch memory_guard_mode. Filling in numbers is
// not the same as turning enforcement on, and conflating them would start
// killing agents on a click the operator read as "fill in the blanks".
func applySuggestedMemoryHandler(c *tool.Ctx) {
	if !requireAdmin(c) {
		return
	}
	total, ok := sysmem.Total()
	if !ok {
		c.JSON(http.StatusBadRequest, map[string]string{
			"error": "cannot read machine memory on this platform; set the limits by hand",
		})
		return
	}
	maxConc := memGuardInt("max_concurrent")
	if maxConc < 1 {
		maxConc = 1
	}
	d := agentconfig.DeriveMemoryDefaults(total, maxConc)

	// A measured peak beats an arithmetic guess: the RAM-derived default
	// only knows the machine, while the peak knows this workload. Applied
	// with 30% headroom, because a limit set exactly at the observed peak
	// kills the next run that does slightly more work — the failure that
	// makes operators distrust the guard and switch it off.
	if d.AgentMaxMB, _ = peakDerivedAgentMB(d.AgentMaxMB); d.AgentMaxMB > d.AgentsTotalMB {
		// Never suggest a per-agent ceiling above the whole agent budget;
		// that would let one agent claim memory the machine cannot spare.
		d.AgentMaxMB = d.AgentsTotalMB
	}

	ctx := c.Context()
	writes := map[string]int{
		"agent_memory_max_mb":    d.AgentMaxMB,
		"agents_total_memory_mb": d.AgentsTotalMB,
		"tool_memory_max_mb":     d.ToolMaxMB,
		"min_free_memory_mb":     d.MinFreeMB,
	}
	for key, val := range writes {
		if err := globalConfigs.SetOwned(ctx, "agents", key, strconv.Itoa(val)); err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true, "applied": d})
}

// peakDerivedAgentMB turns the largest observed agent tree into a ceiling
// with 30% headroom, falling back to the caller's arithmetic default when
// no history exists.
//
// The bool reports whether the number came from measurement, so callers
// can say which it was rather than presenting a guess as an observation.
func peakDerivedAgentMB(fallbackMB int) (int, bool) {
	if resourceHistory == nil {
		return fallbackMB, false
	}
	var worst uint64
	for _, p := range resourceHistory.Peaks() {
		if p.RSSBytes > worst {
			worst = p.RSSBytes
		}
	}
	// Shared with the CLI's report so both suggest the same number, and so
	// the byte-precision arithmetic lives in exactly one place.
	if got := agentconfig.SuggestLimitMB(worst); got > 0 {
		return got, true
	}
	return fallbackMB, false
}

// memGuardConfig reads one agents config value, falling back to def when
// unset so a fresh install reports its real (off) state rather than "".
func memGuardConfig(key, def string) string {
	if globalConfigs == nil {
		return def
	}
	if v := globalConfigs.GetOwned("agents", key); v != "" {
		return v
	}
	return def
}

func memGuardInt(key string) int {
	if globalConfigs == nil {
		return 0
	}
	n, _ := strconv.Atoi(globalConfigs.GetOwned("agents", key))
	return n
}
