package agents

import (
	"fmt"
	"net/http"

	"github.com/yogasw/wick/internal/agents/session"
	agentstore "github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/pkg/tool"
)

// sessionListCap bounds the /api/sessions payload — the list UI shows recent
// sessions, not the full history, so there's no point shipping all of them.
const sessionListCap = 50

// SessionListItem is the JSON shape for one session in the /api/sessions list.
type SessionListItem struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Status      string `json:"status"`
	ProjectID   string `json:"project_id"`
	ActiveAgent string `json:"active_agent"`
	CreatedAt   string `json:"created_at"`
	LastActive  string `json:"last_active"`
	Lifecycle   string `json:"lifecycle"`
	PID         int    `json:"pid,omitempty"`
}

// SessionMetaDTO is the JSON shape returned by /api/sessions/{id}/meta.
type SessionMetaDTO struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Status      string `json:"status"`
	ProjectID   string `json:"project_id"`
	ActiveAgent string `json:"active_agent"`
	TitleCustom bool   `json:"title_custom"`
	CreatedAt   string `json:"created_at"`
	LastActive  string `json:"last_active"`
}

// accessibleSessionIDs returns the subset of ids whose sessions pass the
// project scope filter and the caller's project-access check. Shares the
// projectAccess decision so JSON endpoints match the templ sidebar exactly.
//
// scoped: when non-empty, only sessions whose Meta.ProjectID == scoped pass.
func accessibleSessionIDs(ids []string, sessions map[string]session.Session, access projectAccess, scoped string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		s, ok := sessions[id]
		if !ok {
			continue
		}
		if scoped != "" && s.Meta.ProjectID != scoped {
			continue
		}
		if !access.allowSession(s.Meta.ProjectID, s.Meta.UserID) {
			continue
		}
		out = append(out, id)
	}
	return out
}

// apiSessionList handles GET /api/sessions and returns a JSON list of
// sessions the caller is allowed to see.
func apiSessionList(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	scoped := c.Query("project")
	if scoped != "" {
		if _, ok := globalMgr.Registry().Project(scoped); !ok {
			scoped = ""
		}
	}
	access := callerProjectAccess(c)
	allSessions := globalMgr.Registry().Sessions()
	ids := accessibleSessionIDs(globalMgr.Registry().SessionIDs(), allSessions, access, scoped)
	if len(ids) > sessionListCap {
		ids = ids[:sessionListCap]
	}

	lcBySession := make(map[string]struct {
		Lifecycle string
		PID       int
	}, len(ids))
	if globalPool != nil {
		for _, e := range globalPool.ActiveSnapshot() {
			lcBySession[e.SessionID] = struct {
				Lifecycle string
				PID       int
			}{e.Lifecycle, e.PID}
		}
	}

	items := make([]SessionListItem, 0, len(ids))
	for _, id := range ids {
		s := allSessions[id]
		label := loadFirstUserMessage(globalLayout, id, 60)
		lc := lcBySession[id]
		items = append(items, SessionListItem{
			ID:          id,
			Label:       label,
			Status:      string(s.Meta.Status),
			ProjectID:   s.Meta.ProjectID,
			ActiveAgent: s.Meta.ActiveAgent,
			CreatedAt:   s.Meta.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			LastActive:  s.Meta.LastActive.Format("2006-01-02T15:04:05Z07:00"),
			Lifecycle:   lc.Lifecycle,
			PID:         lc.PID,
		})
	}

	c.JSON(http.StatusOK, map[string][]SessionListItem{"sessions": items})
}

// apiSessionConversation handles GET /api/sessions/{id}/conversation and
// returns all ConversationTurn entries for the session.
func apiSessionConversation(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	sess, ok := globalMgr.Registry().Session(id)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if !callerProjectAccess(c).allowSession(sess.Meta.ProjectID, sess.Meta.UserID) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	turns, err := loadConversation(globalLayout, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": fmt.Errorf("load conversation: %w", err).Error()})
		return
	}
	if turns == nil {
		turns = []agentstore.ConversationTurn{}
	}
	resolveLabelFromTurns(globalLayout, id, turns)
	c.JSON(http.StatusOK, map[string][]agentstore.ConversationTurn{"turns": turns})
}

// apiSessionMeta handles GET /api/sessions/{id}/meta and returns the
// session's metadata as a SessionMetaDTO.
func apiSessionMeta(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	sess, ok := globalMgr.Registry().Session(id)
	if !ok {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if !callerProjectAccess(c).allowSession(sess.Meta.ProjectID, sess.Meta.UserID) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	label := loadFirstUserMessage(globalLayout, id, 60)
	dto := SessionMetaDTO{
		ID:          id,
		Label:       label,
		Status:      string(sess.Meta.Status),
		ProjectID:   sess.Meta.ProjectID,
		ActiveAgent: sess.Meta.ActiveAgent,
		TitleCustom: sess.Meta.TitleCustom,
		CreatedAt:   sess.Meta.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		LastActive:  sess.Meta.LastActive.Format("2006-01-02T15:04:05Z07:00"),
	}
	c.JSON(http.StatusOK, dto)
}
