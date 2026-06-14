package agents

import (
	"net/http"
	"time"

	"github.com/yogasw/wick/internal/login"
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
}

// apiOverview handles GET /api/overview and returns the queue + active session
// lists the caller is allowed to see.
func apiOverview(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	caller := login.GetUser(c.Context())

	active := globalPool.ActiveSnapshot()
	projects := globalMgr.Registry().Projects()
	allSessions := globalMgr.Registry().Sessions()

	activeItems := make([]OverviewActiveDTO, 0, len(active))
	for _, e := range active {
		s, ok := allSessions[e.SessionID]
		if !ok {
			continue
		}
		if !callerCanSeeSession(caller, s.Meta) {
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
	queueItems := make([]OverviewQueuedDTO, 0, len(queue))
	for _, q := range queue {
		s, ok := allSessions[q.SessionID]
		if ok && !callerCanSeeSession(caller, s.Meta) {
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
		},
	})
}
