package agents

import (
	"context"
	"errors"
	"net/http"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// globalDelegation is the sub-agent service, wired at boot. nil = the
// feature is not configured; every endpoint then reports an empty list
// rather than an error, so the rail simply never appears.
var globalDelegation *delegation.Service

// SetDelegation wires the delegation service for the HTTP layer.
func SetDelegation(s *delegation.Service) { globalDelegation = s }

// SubAgentItem is one row in the Sub-agents rail panel. Flat and
// string-typed, matching SessionListItem, so the SPA needs no special
// decoding.
type SubAgentItem struct {
	DelegationID   string `json:"delegation_id"`
	ChildSessionID string `json:"child_session_id"`
	ProfileKey     string `json:"profile_key"`
	// Handle is this instance's address inside its tree — what an @mention
	// resolves to. Empty on rows written before handles existed.
	Handle string `json:"handle,omitempty"`
	// Label is the task, truncated for display.
	Label  string `json:"label"`
	Status string `json:"status"`
	// Lifecycle is merged in from the pool's live snapshot; "" when the
	// child has no active process. Same merge apiSessionList does, so
	// there is only ever one source of truth for liveness.
	Lifecycle string `json:"lifecycle"`
	Depth     int    `json:"depth"`
	TurnsUsed int    `json:"turns_used"`
	MaxTurns  int    `json:"max_turns"`
	Result    string `json:"result,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
	// EndedAt is when the delegation reached a terminal status. Absent
	// while it is queued or running, which is exactly how the UI tells
	// "finished 5m ago" apart from "started 5m ago".
	EndedAt string `json:"ended_at,omitempty"`
}

// subAgentLabelRunes caps the task preview shown in the panel.
const subAgentLabelRunes = 60

// sessionSubAgents handles GET /api/sessions/{id}/subagents — the panel
// list and the tab badge for one conversation.
func sessionSubAgents(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	sess, ok := globalMgr.Registry().Session(id)
	if !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if globalDelegation == nil || globalDelegation.Repo == nil {
		c.JSON(http.StatusOK, map[string]any{"subagents": []SubAgentItem{}})
		return
	}

	rows, err := globalDelegation.Repo.ListByParent(c.Context(), id)
	if err != nil {
		log.Ctx(c.Context()).Error().Err(err).Msg("subagents: list failed")
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	lcBySession := map[string]string{}
	if globalPool != nil {
		for _, e := range globalPool.ActiveSnapshot() {
			lcBySession[e.SessionID] = e.Lifecycle
		}
	}

	items := make([]SubAgentItem, 0, len(rows))
	for _, d := range rows {
		item := SubAgentItem{
			DelegationID:   d.ID,
			ChildSessionID: d.ChildSessionID,
			ProfileKey:     d.ProfileKey,
			Handle:         d.Handle,
			Label:          truncateRunes(d.Task, subAgentLabelRunes),
			Status:         d.Status,
			Lifecycle:      lcBySession[d.ChildSessionID],
			Depth:          d.Depth,
			TurnsUsed:      d.TurnsUsed,
			MaxTurns:       d.MaxTurns,
			Result:         truncateRunes(d.Result, 400),
		}
		if !d.StartedAt.IsZero() {
			item.StartedAt = d.StartedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
		}
		if d.EndedAt != nil && !d.EndedAt.IsZero() {
			item.EndedAt = d.EndedAt.UTC().Format("2006-01-02T15:04:05Z07:00")
		}
		items = append(items, item)
	}
	c.JSON(http.StatusOK, map[string]any{"subagents": items})
}

// interruptSubAgent handles POST /api/delegations/{delegationID}/interrupt.
//
// Outcome → HTTP:
//
//	killed / dequeued → 200
//	already_done      → 409  (a benign race; the UI keeps the real result)
//	not permitted     → 403
//	unknown id        → 404
func interruptSubAgent(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	if globalDelegation == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sub-agents are not enabled"})
		return
	}
	actorID, isAdmin := callerIdentity(c)
	out, err := globalDelegation.Interrupt(c.Context(), c.PathValue("delegationID"), actorID, isAdmin)
	writeInterruptOutcome(c, out, err)
}

// interruptAllSubAgents handles POST /api/sessions/{id}/subagents/interrupt-all.
// Stops every live descendant while LEAVING THE LEADER RUNNING, so it can
// react to the partial results it already has.
func interruptAllSubAgents(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	sess, ok := globalMgr.Registry().Session(id)
	if !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if globalDelegation == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sub-agents are not enabled"})
		return
	}
	actorID, isAdmin := callerIdentity(c)
	n, err := globalDelegation.InterruptAll(c.Context(), id, actorID, isAdmin)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"stopped": n})
}

func writeInterruptOutcome(c *tool.Ctx, out delegation.InterruptOutcome, err error) {
	switch {
	case err == nil:
		if out == delegation.OutcomeAlreadyDone {
			// 409, not 500: the sub-agent finished on its own a moment
			// before the click. The UI treats this as a normal result and
			// must not show an error toast for it.
			c.JSON(http.StatusConflict, map[string]any{"outcome": string(out)})
			return
		}
		c.JSON(http.StatusOK, map[string]any{"outcome": string(out)})
	case errors.Is(err, delegation.ErrForbidden):
		c.JSON(http.StatusForbidden, map[string]string{"error": "not allowed to stop this sub-agent"})
	case errors.Is(err, delegation.ErrDelegationNotFound):
		c.JSON(http.StatusNotFound, map[string]string{"error": "sub-agent not found"})
	default:
		log.Ctx(c.Context()).Error().Err(err).Msg("subagents: interrupt failed")
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}

// callerIdentity resolves the acting user for interrupt authorization.
func callerIdentity(c *tool.Ctx) (string, bool) {
	u := login.GetUser(c.Context())
	if u == nil {
		return "", false
	}
	return u.ID, u.IsAdmin()
}

// cascadeInterruptChildren stops every live descendant of a session.
// Called on the leader-kill path so killing a conversation does not
// leave orphaned sub-agent processes behind.
//
// Errors are logged, never fatal: failing to stop a child must not stop
// the leader itself from being killed, which is what the user asked for.
func cascadeInterruptChildren(c *tool.Ctx, sessionID string) {
	if globalDelegation == nil {
		return
	}
	actorID, isAdmin := callerIdentity(c)
	// Cascade, not Stop all: detached async sub-agents were fired to
	// outlive this conversation and must survive its teardown.
	if _, err := globalDelegation.CascadeInterrupt(c.Context(), sessionID, actorID, isAdmin); err != nil {
		log.Ctx(c.Context()).Warn().Err(err).Str("session", sessionID).
			Msg("subagents: cascade interrupt failed; killing leader anyway")
	}
}

// parentOfChildSession returns the parent session id when sessionID is a
// sub-agent's isolated session, or "" otherwise. Used to redirect a
// bookmarked child URL to the parent conversation instead of 404ing.
func parentOfChildSession(sessionID string) string {
	if globalMgr == nil {
		return ""
	}
	sess, ok := globalMgr.Registry().Session(sessionID)
	if !ok {
		return ""
	}
	return sess.Meta.ParentSessionID
}

// subAgentStatusesJSON exposes the canonical status list so the
// front-end union can be checked against it rather than hand-maintained.
func subAgentStatusesJSON() []string { return entity.DelegationStatuses }

// rootForSession returns the delegation-tree root a session belongs to,
// or "" when it has no tree. A leader owns the trees it started; a
// sub-agent's own row names its root.
func rootForSession(ctx context.Context, sessionID string) string {
	if globalDelegation == nil || globalDelegation.Repo == nil || sessionID == "" {
		return ""
	}
	if row, err := globalDelegation.Repo.FindByChildSession(ctx, sessionID); err == nil && row != nil {
		return row.RootID
	}
	rows, err := globalDelegation.Repo.ListByParent(ctx, sessionID)
	if err != nil {
		return ""
	}
	for _, r := range rows {
		if r.RootID != "" {
			return r.RootID
		}
	}
	return ""
}

// resetHopsForSession refills the agent-to-agent hop budget for the tree
// this session owns. Best-effort: a failed reset must never fail the
// human's message, which is the actual work being done here.
func resetHopsForSession(c *tool.Ctx, sessionID string) {
	root := rootForSession(c.Context(), sessionID)
	if root == "" {
		return
	}
	if err := globalDelegation.Repo.ResetHops(c.Context(), root); err != nil {
		log.Ctx(c.Context()).Warn().Err(err).Str("root", root).
			Msg("subagents: hop reset failed")
	}
}

// sessionMessages returns the agent-to-agent thread for a session's tree.
//
// Empty rather than 404 when there is no tree: the panel asks for this on
// every conversation, and "nobody has talked yet" is a normal state.
func sessionMessages(c *tool.Ctx) {
	root := rootForSession(c.Context(), c.PathValue("id"))
	if root == "" || globalDelegation == nil {
		c.JSON(http.StatusOK, map[string]any{"messages": []any{}, "hops_left": 0})
		return
	}
	msgs, err := globalDelegation.Repo.ListThread(c.Context(), root, 200)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"messages":  msgs,
		"hops_left": globalDelegation.HopsLeft(c.Context(), root),
	})
}

// resetSessionHops refills the hop budget from the UI.
//
// Human-only by construction: it lives on the HTTP surface a person
// clicks, and there is no matching connector op, so an agent cannot lift
// its own limit.
func resetSessionHops(c *tool.Ctx) {
	root := rootForSession(c.Context(), c.PathValue("id"))
	if root == "" || globalDelegation == nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "no sub-agent conversation for this session"})
		return
	}
	if err := globalDelegation.Repo.ResetHops(c.Context(), root); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"ok": true})
}
