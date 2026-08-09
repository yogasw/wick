package agents

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/internal/agents/session"
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
	// QueuePosition is the 1-based place this delegation holds in its
	// tree's waiting line, 0 when it is not queued. Computed server-side
	// so the panel never has to infer ordering from timestamps it may
	// have received out of order.
	QueuePosition int `json:"queue_position,omitempty"`
	// Envelope is the sub-agent's structured answer, when it finished.
	// Absent while it is still working.
	Envelope *delegation.ResultEnvelope `json:"envelope,omitempty"`
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
		item.Envelope = delegation.EnvelopeOf(&d)
		if d.Status == entity.DelegationQueued {
			// Only queued rows are queried: QueuePosition costs a row read
			// plus a count, and running rows always answer 0.
			if pos, perr := globalDelegation.Repo.QueuePosition(c.Context(), d.RootID, d.ID); perr == nil {
				item.QueuePosition = pos
			}
		}
		items = append(items, item)
	}
	out := map[string]any{"subagents": items}
	if inc := sessionIncident(c, id); inc != nil {
		out["incident"] = inc
	}
	c.JSON(http.StatusOK, out)
}

// IncidentSummary is the compact incident header the rail shows above the
// agent list. Deliberately not the whole record: the panel answers "is
// this an investigation, and how is it going", and the full state belongs
// to whoever opens it.
type IncidentSummary struct {
	Status        string `json:"status"`
	Iteration     int    `json:"iteration"`
	Summary       string `json:"summary"`
	StopReason    string `json:"stop_reason,omitempty"`
	EvidenceCount int    `json:"evidence_count"`
}

// sessionIncident returns the tree's incident header, or nil when this
// conversation has no investigation — which is most of them.
func sessionIncident(c *tool.Ctx, sessionID string) *IncidentSummary {
	root := rootForSession(c.Context(), sessionID)
	if root == "" {
		return nil
	}
	inc, err := globalDelegation.IncidentForRoot(c.Context(), root)
	if err != nil || inc == nil {
		return nil
	}
	n, err := globalDelegation.CountEvidence(c.Context(), inc.ID)
	if err != nil {
		log.Ctx(c.Context()).Debug().Err(err).Str("incident", inc.ID).
			Msg("subagents: evidence count failed")
	}
	return &IncidentSummary{
		Status: inc.Status, Iteration: inc.Iteration,
		Summary: truncateRunes(inc.Summary, 240), StopReason: inc.StopReason,
		EvidenceCount: n,
	}
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

// continueSubAgent handles POST /api/delegations/{delegationID}/continue.
//
// The human-facing half of the continue op: a person reading a finished
// sub-agent's result can send it back to work in the SAME session rather
// than delegating again and getting a stranger who has to rediscover
// everything the first one learned.
//
// Authorisation is CanInterrupt, applied inside the service — continuing
// someone else's sub-agent is the same authority as stopping it.
//
// Runs in the background always. A foreground continue would hold the
// HTTP request open for however long the sub-agent takes, and a browser
// that gives up mid-run would look, from the caller's side, exactly like
// a failure — while the sub-agent kept working.
func continueSubAgent(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	if globalDelegation == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sub-agents are not enabled"})
		return
	}
	var body struct {
		Task      string `json:"task"`
		MaxTurns  int    `json:"max_turns"`
		MaxTokens int    `json:"max_tokens"`
	}
	if err := c.BindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if strings.TrimSpace(body.Task) == "" {
		c.JSON(http.StatusBadRequest, map[string]string{
			"error": "say what the sub-agent should do next",
		})
		return
	}

	actorID, isAdmin := callerIdentity(c)
	res, err := globalDelegation.Continue(c.Context(), delegation.ContinueRequest{
		DelegationID: c.PathValue("delegationID"),
		Task:         body.Task,
		ExtraTurns:   body.MaxTurns,
		ExtraTokens:  body.MaxTokens,
		Mode:         delegation.ModeAsync,
		ActorID:      actorID,
		IsAdmin:      isAdmin,
	})
	switch {
	case err == nil:
		// `resumed` is the load-bearing field here, not `status`: it tells
		// the UI whether the sub-agent came back with its memory or is
		// starting over inside its old session.
		c.JSON(http.StatusOK, map[string]any{
			"delegation_id": res.DelegationID,
			"status":        res.Status,
			"resumed":       res.Resumed,
			"note":          res.Note,
		})
	case errors.Is(err, delegation.ErrForbidden):
		c.JSON(http.StatusForbidden, map[string]string{"error": "not allowed to continue this sub-agent"})
	case errors.Is(err, delegation.ErrDelegationNotFound):
		c.JSON(http.StatusNotFound, map[string]string{"error": "sub-agent not found"})
	case errors.Is(err, delegation.ErrNotContinuable):
		// 409, like the interrupt race: the sub-agent is working again, so
		// the button the user clicked no longer applies. Its own message
		// names the way out, so pass it through rather than flattening it.
		c.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		log.Ctx(c.Context()).Error().Err(err).Msg("subagents: continue failed")
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
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
	_, survivors, err := globalDelegation.CascadeInterruptReport(c.Context(), sessionID, actorID, isAdmin)
	if err != nil {
		log.Ctx(c.Context()).Warn().Err(err).Str("session", sessionID).
			Msg("subagents: cascade interrupt failed; killing leader anyway")
		return
	}
	// Work that outlived the leader is invisible from a channel thread — the
	// agent goes quiet while the sub-agent keeps going. Say so.
	if len(survivors) > 0 {
		notifyDetachedSurvivors(sessionID, survivors)
	}
}

// notifyDetachedSurvivors tells the channels which sub-agents outlived a killed
// leader. No-op when no channel layer is wired (headless/tests).
func notifyDetachedSurvivors(sessionID string, survivors []delegation.Survivor) {
	if globalChannels == nil {
		return
	}
	out := make([]agentchannels.DetachedSurvivor, 0, len(survivors))
	for _, sv := range survivors {
		out = append(out, agentchannels.DetachedSurvivor{
			Handle: sv.Handle, ProfileKey: sv.ProfileKey,
		})
	}
	globalChannels.DispatchDetachedSurvivors(sessionID, out)
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

// humanMentionNote is the line appended to a person's message telling the
// leader which of its mentions wick is taking.
//
// Synchronous on purpose, and cheap on purpose: it only resolves the
// roster, so it costs two queries and never a spawn. The dispatch itself
// still runs detached below — a role whose default mode is sync would
// otherwise hold the person's POST open for the whole sub-agent run.
func humanMentionNote(ctx context.Context, sess session.Session, sessionID, text string) string {
	if globalDelegation == nil || globalDelegation.Repo == nil || strings.TrimSpace(text) == "" {
		return ""
	}
	return globalDelegation.PreRouteNote(ctx, delegation.RouteInput{
		SessionID:  sessionID,
		ProjectID:  sess.Meta.ProjectID,
		FromHandle: entity.LeaderHandle,
		Human:      true,
		Text:       text,
	})
}

// routeHumanMentions acts on @names a PERSON wrote in a session.
//
// Runs detached and best-effort: routing is a side channel, and the
// message it rode in on has already been delivered. A dispatch that fails
// must never turn a person's message into an error.
//
// The human's own text is never edited, so the leader still sees exactly
// what was typed even when a mention took the work elsewhere.
func routeHumanMentions(ctx context.Context, sess session.Session, sessionID, text string) {
	if globalDelegation == nil || globalDelegation.Repo == nil || strings.TrimSpace(text) == "" {
		return
	}
	go globalDelegation.Route(ctx, delegation.RouteInput{
		SessionID:  sessionID,
		ProjectID:  sess.Meta.ProjectID,
		FromHandle: entity.LeaderHandle,
		Human:      true,
		Text:       text,
		// A human turn is attributed to the session's owner, which is what
		// the sub-agent's tool access is then narrowed from.
		TriggeredBy: sess.Meta.UserID,
	})
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
