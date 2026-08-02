package agents

import (
	"net/http"
	"sort"
	"time"

	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// Fleet monitor — a read-only view of every agent process alive right
// now, plus the delegation history behind them.
//
// Pure consumer: no new storage, no new SSE hub. Live state comes from
// pool.ActiveSnapshot(), history from agent_delegations, and updates ride
// the existing /stream broadcaster. The one deliberate exception to
// "read-only" is interrupt, which lives on the delegation endpoints —
// stopping work is not the same as steering it.

// MonitorAgent is one live agent process in the fleet view.
type MonitorAgent struct {
	SessionID    string `json:"session_id"`
	AgentName    string `json:"agent_name"`
	ProviderType string `json:"provider_type"`
	ProviderName string `json:"provider_name"`
	PID          int    `json:"pid"`
	Lifecycle    string `json:"lifecycle"`
	Substate     string `json:"substate"`
	Queued       int    `json:"queued"`
	LastActive   string `json:"last_active,omitempty"`
	// Status is the UI badge: running | idle | spawning | dead.
	Status string `json:"status"`
	// IsSubAgent marks a process that belongs to a delegation rather than
	// a human-facing conversation.
	IsSubAgent bool   `json:"is_sub_agent"`
	ProfileKey string `json:"profile_key,omitempty"`
	// DelegationID lets the UI offer Stop for a sub-agent row.
	DelegationID string `json:"delegation_id,omitempty"`
	RootID       string `json:"root_id,omitempty"`
}

// MonitorTree summarises one delegation tree. Cheap cost signals — total
// turns, wall-clock, how many sub-agents — so a tree that is running away
// is obvious at a glance even before token accounting exists.
type MonitorTree struct {
	RootID     string `json:"root_id"`
	RootTask   string `json:"root_task"`
	SubAgents  int    `json:"sub_agents"`
	TurnsUsed  int    `json:"turns_used"`
	TokensUsed int    `json:"tokens_used,omitempty"`
	CostUSD    string `json:"cost_usd,omitempty"`
	Active     int    `json:"active"`
	StartedAt  string `json:"started_at,omitempty"`
	ElapsedSec int    `json:"elapsed_sec"`
	Status     string `json:"status"`
}

// monitorStatus maps a pool lifecycle onto the fleet badge. A PID of 0
// on a provider that does not respawn per turn means the process is gone
// even if the lifecycle string has not caught up.
func monitorStatus(lifecycle string, pid int, respawns bool) string {
	switch lifecycle {
	case "killed":
		return "dead"
	case "spawning":
		return "spawning"
	}
	if pid == 0 && !respawns {
		return "dead"
	}
	switch lifecycle {
	case "working":
		return "running"
	case "idle":
		return "idle"
	}
	return "idle"
}

// apiMonitorSnapshot handles GET /api/monitor/snapshot.
//
// Visibility follows the same rule as interrupt: an admin sees the whole
// fleet, everyone else sees only the delegations they triggered. A
// non-admin must not learn what other people's agents are doing.
func apiMonitorSnapshot(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	u := login.GetUser(c.Context())
	if u == nil {
		c.JSON(http.StatusForbidden, map[string]string{"error": "not signed in"})
		return
	}
	isAdmin := u.IsAdmin()

	// Index delegations by child session so a live process can be
	// labelled as a sub-agent and carry a Stop target.
	type delMeta struct {
		id, profile, root, triggeredBy string
	}
	bySession := map[string]delMeta{}
	trees := map[string]*MonitorTree{}

	if globalDelegation != nil && globalDelegation.Repo != nil {
		rows, err := globalDelegation.Repo.ListRecent(c.Context(), 500)
		if err != nil {
			c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		for _, d := range rows {
			if !isAdmin && d.TriggeredBy != u.ID {
				continue
			}
			bySession[d.ChildSessionID] = delMeta{d.ID, d.ProfileKey, d.RootID, d.TriggeredBy}

			t := trees[d.RootID]
			if t == nil {
				t = &MonitorTree{RootID: d.RootID, Status: "done"}
				trees[d.RootID] = t
			}
			t.SubAgents++
			t.TurnsUsed += d.TurnsUsed
			t.TokensUsed += d.TokensUsed
			if d.Depth == 0 {
				t.RootTask = truncateRunes(d.Task, 80)
			}
			if !d.StartedAt.IsZero() {
				if t.StartedAt == "" || d.StartedAt.UTC().Format(time.RFC3339) < t.StartedAt {
					t.StartedAt = d.StartedAt.UTC().Format(time.RFC3339)
					t.ElapsedSec = int(time.Since(d.StartedAt).Seconds())
				}
			}
			if !entity.IsTerminalDelegationStatus(d.Status) {
				t.Active++
				t.Status = "running"
			}
		}
	}

	agents := make([]MonitorAgent, 0, 16)
	if globalPool != nil {
		for _, e := range globalPool.ActiveSnapshot() {
			meta, isSub := bySession[e.SessionID]
			// A non-admin sees their own conversations' agents plus their
			// own sub-agents — never someone else's fleet.
			if !isAdmin && !isSub {
				if sess, ok := globalMgr.Registry().Session(e.SessionID); !ok || sess.Meta.UserID != u.ID {
					continue
				}
			}
			m := MonitorAgent{
				SessionID: e.SessionID, AgentName: e.AgentName,
				ProviderType: e.ProviderType, ProviderName: e.ProviderName,
				PID: e.PID, Lifecycle: e.Lifecycle, Substate: e.Substate,
				Queued:     e.Queued,
				Status:     monitorStatus(e.Lifecycle, e.PID, e.Respawns),
				IsSubAgent: isSub,
			}
			if !e.LastActive.IsZero() {
				m.LastActive = e.LastActive.UTC().Format(time.RFC3339)
			}
			if isSub {
				m.ProfileKey = meta.profile
				m.DelegationID = meta.id
				m.RootID = meta.root
			}
			agents = append(agents, m)
		}
	}
	// Stable order: busiest first, then by session, so the grid does not
	// reshuffle on every refresh.
	sort.Slice(agents, func(i, j int) bool {
		if agents[i].Status != agents[j].Status {
			return statusRank(agents[i].Status) < statusRank(agents[j].Status)
		}
		return agents[i].SessionID < agents[j].SessionID
	})

	treeList := make([]MonitorTree, 0, len(trees))
	for _, t := range trees {
		if t.CostUSD == "" && t.TokensUsed > 0 {
			t.CostUSD = delegation.FormatCostUSD(delegation.EstimateCostUSD(t.TokensUsed))
		}
		treeList = append(treeList, *t)
	}
	sort.Slice(treeList, func(i, j int) bool {
		if treeList[i].Active != treeList[j].Active {
			return treeList[i].Active > treeList[j].Active
		}
		return treeList[i].StartedAt > treeList[j].StartedAt
	})

	c.JSON(http.StatusOK, map[string]any{
		"agents": agents,
		"trees":  treeList,
	})
}

func statusRank(s string) int {
	switch s {
	case "running":
		return 0
	case "spawning":
		return 1
	case "idle":
		return 2
	default:
		return 3
	}
}
