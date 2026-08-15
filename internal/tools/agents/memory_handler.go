package agents

import (
	"net/http"
	"strconv"

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

	procs, err := memreport.Snapshot()
	rep.ProcessesReadable = err == nil
	if err == nil {
		for _, r := range memreport.Roots(procs, agentProcessNames) {
			row := memoryAgentRow{
				Name:      r.Name,
				PID:       r.PID,
				TreeBytes: memreport.SumSubtree(procs, r.PID),
			}
			if big := memreport.LargestDescendant(procs, r.PID); big.PID != 0 {
				row.LargestName, row.LargestBytes = big.Name, big.RSSBytes
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
