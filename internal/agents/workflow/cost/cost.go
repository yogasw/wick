// Package cost aggregates per-node + per-workflow LLM usage. Populated
// during a run by classify/agent executors (which receive Usage from
// the provider) and surfaced via `workflow_get_run`.
package cost

import (
	"sync"

	"github.com/yogasw/wick/internal/agents/workflow/provider"
)

// Tracker is the in-memory aggregator. One per Manager instance.
type Tracker struct {
	mu     sync.Mutex
	byNode map[string]provider.Usage
	byRun  map[string]provider.Usage
	byDay  map[string]provider.Usage
}

// New builds an empty tracker.
func New() *Tracker {
	return &Tracker{
		byNode: map[string]provider.Usage{},
		byRun:  map[string]provider.Usage{},
		byDay:  map[string]provider.Usage{},
	}
}

// Record adds usage from one node execution.
func (c *Tracker) Record(runID, nodeID, day string, u provider.Usage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byNode[runID+"/"+nodeID] = u
	c.byRun[runID] = add(c.byRun[runID], u)
	c.byDay[day] = add(c.byDay[day], u)
}

// PerRun returns totals for a run_id.
func (c *Tracker) PerRun(runID string) provider.Usage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byRun[runID]
}

// PerDay returns totals for a date.
func (c *Tracker) PerDay(day string) provider.Usage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byDay[day]
}

// PerNode returns usage for a specific run+node.
func (c *Tracker) PerNode(runID, nodeID string) provider.Usage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.byNode[runID+"/"+nodeID]
}

func add(a, b provider.Usage) provider.Usage {
	return provider.Usage{
		InputTokens:  a.InputTokens + b.InputTokens,
		OutputTokens: a.OutputTokens + b.OutputTokens,
		CostUSD:      a.CostUSD + b.CostUSD,
		LatencyMs:    a.LatencyMs + b.LatencyMs,
	}
}
