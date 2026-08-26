package agents

import (
	"fmt"
	"net/http"
	"strconv"

	agentsconfig "github.com/yogasw/wick/internal/agents/config"
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
	// Provider is the active agent's "type/name" provider key — distinct
	// from ActiveAgent (the agent's own name, e.g. "main"). Empty if no
	// agent entry exists yet.
	Provider string `json:"provider,omitempty"`
	// ModelID is the active agent's pinned model id (currently meaningful
	// for wick only). Empty = that provider's own default model.
	ModelID string `json:"model_id,omitempty"`
	// Widget is the already-resolved HTML-artifact CSP policy (global,
	// with this session's project override applied). The SPA builds the
	// iframe CSP from it and never resolves anything itself. Always sent,
	// never omitted: an absent field would leave the SPA guessing, and
	// the safe guess and the real policy are not always the same.
	Widget agentsconfig.WidgetPolicy `json:"widget"`
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
		// Sub-agent sessions are real sessions but belong to their
		// parent's Sub-agents panel, not the conversation list. Filtered
		// HERE rather than after the list cap: a leader that fans out to
		// many sub-agents would otherwise push legitimate conversations
		// out of the capped window.
		//
		// Only the JSON/sidebar view filters. session.List stays
		// unfiltered so reapers, sweepers and migrations keep seeing
		// every session.
		if s.Meta.ParentSessionID != "" {
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

// backfillTurnIDs assigns a synthetic, position-stable id to every turn that
// was persisted without one (user turns, and anything written before turn_id
// existed). The conversation file is append-only, so the index is stable
// across loads — which is all the pagination cursor and the SPA's dedup need.
// Real ids are numeric timestamps, so the "turn-" prefix can't collide.
func backfillTurnIDs(turns []agentstore.ConversationTurn) {
	for i := range turns {
		if turns[i].TurnID == "" {
			turns[i].TurnID = fmt.Sprintf("turn-%d", i)
		}
	}
}

// pageTurns returns the window of turns ending just before the turn with id
// `before` (empty = end of history), holding at most `limit` entries (0 = no
// cap). hasMore reports whether older turns remain before the window. An
// unknown `before` falls back to the latest window rather than erroring — the
// turn may have been pruned (e.g. collapsed provider-switch turns) between
// the client's fetches.
func pageTurns(turns []agentstore.ConversationTurn, before string, limit int) ([]agentstore.ConversationTurn, bool) {
	end := len(turns)
	if before != "" {
		for i, t := range turns {
			if t.TurnID == before {
				end = i
				break
			}
		}
	}
	start := 0
	if limit > 0 && end-limit > 0 {
		start = end - limit
	}
	return turns[start:end], start > 0
}

// apiSessionConversation handles GET /api/sessions/{id}/conversation and
// returns the session's ConversationTurn entries. With no query params it
// returns the full history; `?limit=N` returns only the latest N turns and
// `&before=<turn_id>` walks back one window at a time (infinite scroll up).
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
	// Label derives from the FIRST user message, so it must see the full
	// history — page after it, not before.
	resolveLabelFromTurns(globalLayout, id, turns)
	backfillTurnIDs(turns)
	limit, _ := strconv.Atoi(c.Query("limit"))
	page, hasMore := pageTurns(turns, c.Query("before"), limit)
	if cwd, err := resolveSessionCwd(sess); err == nil {
		attachArtifactsToTurns(globalLayout, id, c.Base(), cwd, page)
	}
	c.JSON(http.StatusOK, map[string]any{"turns": page, "has_more": hasMore})
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
		Widget:      resolveWidgetPolicy(sess.Meta.ProjectID),
	}
	// Resolve provider + pinned model from the active (or first) agent
	// entry — ActiveAgent above is the agent's own NAME ("main"), not its
	// provider key, so the composer needs this separately.
	agentName := sess.Meta.ActiveAgent
	if agentName == "" && len(sess.Agents) > 0 {
		agentName = sess.Agents[0].Name
	}
	for _, a := range sess.Agents {
		if a.Name == agentName {
			dto.Provider = a.Provider
			dto.ModelID = a.ModelID
			break
		}
	}
	c.JSON(http.StatusOK, dto)
}
