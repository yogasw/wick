package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/yogasw/wick/internal/agents/a2aremote"
	"github.com/yogasw/wick/internal/agents/remote/slackremote"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// Wiring of the Team A2A link (internal/agents/teamlink) onto the pool.

// sourceTeam marks a turn wick posted on a teammate's behalf, so the
// front-end badges it instead of showing it as the person typing.
const sourceTeam = "team"

// NewTeamLinkHub builds the Hub over the Team service and the pool.
// deliver wakes a session with a late reply (the sub-agent delivery path).
func NewTeamLinkHub(svc *team.Service, deliver func(ctx context.Context, sessionID, text string) error) *teamlink.Hub {
	return teamlink.NewHub(teamDirectory{svc: svc}, poolTurns{}, teamNotifier{deliver: deliver})
}

// TeamAgentOf returns the Team agent a session belongs to, "" for none.
func TeamAgentOf(ctx context.Context, sessionID string) string {
	if p := globalTeam.AgentFor(ctx, sessionID); p != nil {
		return p.ID
	}
	return ""
}

// globalTeamHub is the server's late-bound Hub, set by SetTeamHub; nil
// until wiring runs (and on installs without Team).
var globalTeamHub func() *teamlink.Hub

// SetTeamHub hands the Hub accessor to the HTTP handlers.
func SetTeamHub(f func() *teamlink.Hub) { globalTeamHub = f }

// sessionTeamTasks handles GET /api/sessions/{id}/team-tasks: the
// team_message / @mention tasks this session sent, for the Sub-agents
// panel's Team section. Read-only; the session must be the caller's.
func sessionTeamTasks(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	sess, ok := globalMgr.Registry().Session(id)
	if !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	var tasks []teamlink.TaskView
	if globalTeamHub != nil {
		if h := globalTeamHub(); h != nil {
			tasks = h.SentFrom(id)
		}
	}
	if tasks == nil {
		tasks = []teamlink.TaskView{}
	}
	c.JSON(http.StatusOK, map[string]any{"tasks": tasks})
}

type teamDirectory struct{ svc *team.Service }

func (d teamDirectory) peer(p entity.AgentPersona) teamlink.Peer {
	m := d.svc.MemberOf(p)
	return teamlink.Peer{
		ID: p.ID, OwnerID: p.OwnerUserID, Handle: p.Handle,
		Name: m.Name, Tagline: m.Tagline, Description: m.Description,
		IsCaptain: p.IsCaptain, Disabled: p.Disabled,
		MentionFrom: teamlink.NormalizeMentionFrom(p.MentionFrom), MentionAllow: decodeIDList(p.MentionAllow),
		MaxHops: p.MaxHops,
		Remote:  IsRemoteAgent(p), RemoteOwnerOnly: remoteOwnerOnly(p),
	}
}

// remoteOwnerOnly reports whether p is a remote agent its owner keeps to
// themselves. Missing or unreadable settings count as owner-only.
func remoteOwnerOnly(p entity.AgentPersona) bool {
	if !IsRemoteAgent(p) {
		return false
	}
	if isSlackRemote(p) {
		if slackRemoteStore() == nil {
			return true
		}
		cfg, ok, err := slackRemoteStore().Load(p.ID)
		return err != nil || !ok || cfg.EffectiveUsage() != slackremote.UsageMeAndAgents
	}
	if remoteStore() == nil {
		return true
	}
	cfg, ok, err := remoteStore().Load(p.ID)
	return err != nil || !ok || cfg.EffectiveUsage() != a2aremote.UsageMeAndAgents
}

func (d teamDirectory) Peers(ctx context.Context, ownerID string) ([]teamlink.Peer, error) {
	all, err := d.svc.List(ctx, ownerID)
	if err != nil {
		return nil, err
	}
	out := make([]teamlink.Peer, 0, len(all))
	for _, p := range all {
		out = append(out, d.peer(p))
	}
	return out, nil
}

func (d teamDirectory) Get(ctx context.Context, agentID string) (teamlink.Peer, error) {
	p, err := d.svc.Get(ctx, agentID)
	if err != nil {
		return teamlink.Peer{}, err
	}
	return d.peer(p), nil
}

// poolTurns runs a turn in the agent's main conversation: queued behind
// whatever it is doing, spawned as the agent (persona, scope and run_as
// are applied by the ordinary spawn path, which keys off the session).
type poolTurns struct{}

func (poolTurns) Run(ctx context.Context, agent teamlink.Peer, text string) (string, string, error) {
	s, ok := mainSessionOf(agent.OwnerID, agent.ID)
	if !ok {
		return "", "", fmt.Errorf("@%s has no main chat yet — its owner has to open it once in the Agents app", agent.Handle)
	}
	// Subscribe BEFORE sending, or a fast turn ends unseen.
	ch, unsub := NewDelegationStream(globalBcast).SubscribeSession(s.ID)
	defer unsub()
	// WithoutCancel: the turn must outlive the call that carried it.
	if err := globalPool.Send(context.WithoutCancel(ctx), s.ID, "", sourceTeam, "user", text); err != nil {
		return s.ID, "", err
	}
	return s.ID, collectTurn(ctx, ch), nil
}

// MainSession is the session a turn of agent runs in (SessionLocator).
func (poolTurns) MainSession(_ context.Context, agent teamlink.Peer) string {
	if s, ok := mainSessionOf(agent.OwnerID, agent.ID); ok {
		return s.ID
	}
	return ""
}

// collectTurn joins the text of one turn, up to its Done.
func collectTurn(ctx context.Context, ch <-chan delegation.StreamEvent) string {
	var b strings.Builder
	for {
		select {
		case <-ctx.Done():
			return b.String()
		case ev, ok := <-ch:
			if !ok {
				return b.String()
			}
			switch ev.Type {
			case event.TextDelta:
				b.WriteString(ev.Text)
			case event.Done:
				return b.String()
			}
		}
	}
}

type teamNotifier struct {
	deliver func(ctx context.Context, sessionID, text string) error
}

func (n teamNotifier) Deliver(ctx context.Context, sessionID, text string) error {
	if n.deliver == nil {
		return nil
	}
	return n.deliver(ctx, sessionID, text)
}

// Audit logs one mention_handoff per side, records it in that thread as
// a kind:"mention_handoff" system turn and pushes the same turn to open
// viewers as a mention_handoff SSE event, so the row shows without a
// reload. A task is audited twice (working, then its final state); the
// front-end folds the turns of one task_id into one row.
func (teamNotifier) Audit(_ context.Context, sessionID string, h teamlink.Handoff) {
	log.Info().Str("event", "mention_handoff").Str("session", sessionID).
		Str("from", h.From).Str("to", h.To).Str("context_id", h.ContextID).
		Str("task_id", h.TaskID).Str("state", string(h.State)).Msg("team: handoff")
	recordSystemTurn(globalLayout, globalBcast, sessionID, handoffTurn(h, time.Now()))
}

// Refused records a message the Hub would not send in the caller's
// thread: hop_limit when the exchange's budget ran out, mention_refused
// when an @mention named nobody who takes one.
func (teamNotifier) Refused(_ context.Context, r teamlink.Refusal) {
	recordSystemTurn(globalLayout, globalBcast, r.Session, refusalTurn(r, time.Now()))
}

// refusalTurn is r as a conversation system turn.
func refusalTurn(r teamlink.Refusal, now time.Time) store.ConversationTurn {
	extras := map[string]string{"from": r.From, "to": r.To}
	if r.Err != nil {
		extras["reason"] = r.Err.Error()
	}
	if r.HopLimit {
		extras["context_id"] = r.ContextID
		limit := r.MaxTurns
		if limit <= 0 {
			limit = teamlink.MaxContextTurns
		}
		return hopLimitTurn(extras, limit, now)
	}
	return systemTurn(store.KindMentionRefused, fmt.Sprintf("@%s doesn't take mentions", r.To), extras, now)
}

// hopLimitTurn is the hop_limit event for a budget of limit turns.
func hopLimitTurn(extras map[string]string, limit int, now time.Time) store.ConversationTurn {
	extras["max_turns"] = fmt.Sprintf("%d", limit)
	return systemTurn(store.KindHopLimit, fmt.Sprintf("Agent-to-agent limit of %d turns reached — reply to continue", limit), extras, now)
}

// decodeIDList reads a JSON array of ids; a malformed value reads as none.
func decodeIDList(raw string) []string {
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// encodeIDList is ids as a JSON array, "[]" for none.
func encodeIDList(ids []string) string {
	if len(ids) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(ids)
	return string(b)
}

// handoffTurn is h as a conversation system turn.
func handoffTurn(h teamlink.Handoff, now time.Time) store.ConversationTurn {
	return systemTurn(store.KindMentionHandoff, fmt.Sprintf("@%s → @%s · %s", h.From, h.To, h.State), map[string]string{
		"from": h.From, "to": h.To, "to_agent_id": h.ToID, "state": string(h.State),
		"task_id": h.TaskID, "context_id": h.ContextID,
	}, now)
}

// publishHandoff pushes turn to sessionID's live viewers.
func publishHandoff(b *Broadcaster, sessionID string, turn store.ConversationTurn) {
	publishSystemTurnEvent(b, sessionID, turn)
}

// appendHandoff writes turn into sessionID's conversation.
func appendHandoff(layout agentconfig.Layout, sessionID string, turn store.ConversationTurn) error {
	if layout.BaseDir == "" || sessionID == "" {
		return nil
	}
	return storage.AppendJSONL(layout.SessionConversation(sessionID), "wick-conv-v1", sessionID, turn)
}

// TeamMentionRouter adapts the Hub to delegation.TeamRouter: an @handle
// line naming a teammate is sent over the same client team_message uses,
// without waiting.
type TeamMentionRouter struct{ Hub func() *teamlink.Hub }

func (r TeamMentionRouter) TeamHandles(ctx context.Context, sessionID string) []string {
	h, id := r.Hub(), TeamAgentOf(ctx, sessionID)
	if h == nil || id == "" {
		return nil
	}
	peers, err := h.Reachable(ctx, id)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(peers))
	for _, p := range peers {
		out = append(out, p.Handle)
	}
	return out
}

func (r TeamMentionRouter) SendTeam(ctx context.Context, sessionID, handle, body string, human bool) error {
	h := r.Hub()
	if h == nil {
		return nil
	}
	_, err := h.Send(context.WithoutCancel(ctx), teamlink.SendInput{
		CallerSession: sessionID, CallerAgentID: TeamAgentOf(ctx, sessionID),
		To: handle, Text: body, Wait: -1, Mention: true, Human: human,
	})
	return err
}
