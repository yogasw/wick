package agents

import (
	"net/http"
	"time"

	"github.com/yogasw/wick/internal/agents/resourceguard"
	"github.com/yogasw/wick/pkg/tool"
)

// OverviewQueuedDTO is one row in the queue panel returned by /api/overview.
type OverviewQueuedDTO struct {
	SessionID string `json:"session_id"`
	AgentName string `json:"agent_name"`
	WaitingMs int64  `json:"waiting_ms"`
	Label     string `json:"label"`
	Project   string `json:"project"`
}

// OverviewActiveDTO is one row in the active sessions panel returned by /api/overview.
type OverviewActiveDTO struct {
	SessionID string `json:"session_id"`
	Label     string `json:"label"`
	Lifecycle string `json:"lifecycle"`
	PID       int    `json:"pid,omitempty"`
	ProjectID string `json:"project_id"`
}

// OverviewDTO is the JSON body returned by GET /api/overview.
type OverviewDTO struct {
	Queued []OverviewQueuedDTO `json:"queued"`
	Active []OverviewActiveDTO `json:"active"`
	Stats  OverviewStatsDTO    `json:"stats"`
}

// OverviewStatsDTO carries the pool counters for the stats row.
type OverviewStatsDTO struct {
	Active   int `json:"active"`
	PoolMax  int `json:"pool_max"`
	QueueLen int `json:"queue_len"`
	// QueueReason says in plain words why sessions are waiting, so the
	// Queue card can explain itself. nil when nothing is queued.
	QueueReason *OverviewQueueReasonDTO `json:"queue_reason,omitempty"`
}

// OverviewQueueReasonDTO is why the queue is not draining, read from the
// same state the pool admits by.
//
// Kind is guard_hold (the resource guard is holding new agents), slots_full
// (every pool slot is taken) or waiting (slots are free, but the free-memory
// floor or a provider's own slot limit is holding the spawn).
type OverviewQueueReasonDTO struct {
	Kind   string    `json:"kind"`
	Detail string    `json:"detail,omitempty"`
	Since  time.Time `json:"since,omitempty"`
	// SafePct is the level CPU and memory must drop under before the
	// guard lets agents start again. Only set for guard_hold.
	SafePct int `json:"safe_pct,omitempty"`
}

// queueReason picks the reason the pool would give right now. The guard
// is checked first: while it holds, free slots do not matter. since is the
// oldest queued entry, the fallback when the guard event is unknown.
func queueReason(queueLen int, hold bool, events []resourceguard.Event, safePct, active, poolMax int, since time.Time) *OverviewQueueReasonDTO {
	if queueLen == 0 {
		return nil
	}
	if hold {
		r := &OverviewQueueReasonDTO{Kind: "guard_hold", Since: since, SafePct: safePct}
		// The newest near_hang that no later resolved closed is what
		// started this hold.
		for i := len(events) - 1; i >= 0; i-- {
			if events[i].Kind == "resolved" {
				break
			}
			if events[i].Kind == "near_hang" {
				r.Detail, r.Since = events[i].Detail, events[i].At
				break
			}
		}
		return r
	}
	if poolMax > 0 && active >= poolMax {
		return &OverviewQueueReasonDTO{Kind: "slots_full", Since: since}
	}
	return &OverviewQueueReasonDTO{Kind: "waiting", Since: since}
}

// apiOverview handles GET /api/overview and returns the queue + active session
// lists the caller is allowed to see.
func apiOverview(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	access := callerProjectAccess(c)

	active := globalPool.ActiveSnapshot()
	projects := globalMgr.Registry().Projects()
	allSessions := globalMgr.Registry().Sessions()

	activeItems := make([]OverviewActiveDTO, 0, len(active))
	for _, e := range active {
		s, ok := allSessions[e.SessionID]
		if !ok {
			continue
		}
		if !access.allowSession(s.Meta.ProjectID, s.Meta.UserID, s.Meta.Participants) {
			continue
		}
		label := loadFirstUserMessage(globalLayout, e.SessionID, 60)
		activeItems = append(activeItems, OverviewActiveDTO{
			SessionID: e.SessionID,
			Label:     label,
			Lifecycle: e.Lifecycle,
			PID:       e.PID,
			ProjectID: s.Meta.ProjectID,
		})
	}

	queue := globalPool.QueueSnapshot()
	now := time.Now()
	oldest := now
	for _, q := range queue {
		if q.Enqueued.Before(oldest) {
			oldest = q.Enqueued
		}
	}
	queueItems := make([]OverviewQueuedDTO, 0, len(queue))
	for _, q := range queue {
		s, ok := allSessions[q.SessionID]
		if ok && !access.allowSession(s.Meta.ProjectID, s.Meta.UserID, s.Meta.Participants) {
			continue
		}
		projName := ""
		if ok && s.Meta.ProjectID != "" {
			if p, ok2 := projects[s.Meta.ProjectID]; ok2 {
				projName = p.Meta.Name
			}
		}
		label := loadFirstUserMessage(globalLayout, q.SessionID, 60)
		queueItems = append(queueItems, OverviewQueuedDTO{
			SessionID: q.SessionID,
			AgentName: q.AgentName,
			WaitingMs: now.Sub(q.Enqueued).Milliseconds(),
			Label:     label,
			Project:   projName,
		})
	}

	c.JSON(http.StatusOK, OverviewDTO{
		Queued: queueItems,
		Active: activeItems,
		Stats: OverviewStatsDTO{
			Active:   globalPool.Active(),
			PoolMax:  globalPool.MaxConcurrent(),
			QueueLen: globalPool.QueueLen(),
			QueueReason: queueReason(globalPool.QueueLen(), resourceGuard.HoldSpawns(), resourceGuard.History(),
				memGuardInt("resource_guard_safe_pct"), globalPool.Active(), globalPool.MaxConcurrent(), oldest),
		},
	})
}
