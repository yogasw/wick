package agents

import (
	"net/http"
	"strconv"
	"time"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider/memscope"
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
	// Peak* are the highest values seen across the retained window — the
	// numbers a limit must accommodate, which a single instant misses.
	PeakBytes  uint64  `json:"peak_bytes,omitempty"`
	PeakCPUPct float64 `json:"peak_cpu_pct,omitempty"`
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

	Agents []memoryAgentRow `json:"agents"`
	// ProcessesReadable is false where /proc does not exist, so the empty
	// Agents list is not mistaken for "nothing is running".
	ProcessesReadable bool `json:"processes_readable"`

	Suggested agentconfig.MemoryDefaults `json:"suggested"`
	Current   memoryCurrentLimits        `json:"current"`

	// History describes the sample buffer so the UI can label its chart
	// ("12 minutes, 48 points") instead of drawing an unbounded axis.
	History memreport.Stats `json:"history"`
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
		rep.Notice = "scope isolation unavailable — systemd user session not reachable; agents run unguarded"
	}

	total, okT := sysmem.Total()
	avail, _ := sysmem.Available()
	rep.TotalBytes, rep.AvailableBytes, rep.MachineKnown = total, avail, okT

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
