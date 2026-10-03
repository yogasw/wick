package agents

import (
	"context"
	"fmt"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/agents/teamlink"
	"github.com/yogasw/wick/internal/entity"
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

type teamDirectory struct{ svc *team.Service }

func (d teamDirectory) peer(p entity.AgentPersona) teamlink.Peer {
	m := d.svc.MemberOf(p)
	return teamlink.Peer{
		ID: p.ID, OwnerID: p.OwnerUserID, Handle: p.Handle,
		Name: m.Name, Tagline: m.Tagline, Description: m.Description,
		IsCaptain: p.IsCaptain, Disabled: p.Disabled,
	}
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

// Audit logs one mention_handoff per side.
func (teamNotifier) Audit(_ context.Context, sessionID string, h teamlink.Handoff) {
	log.Info().Str("event", "mention_handoff").Str("session", sessionID).
		Str("from", h.From).Str("to", h.To).Str("context_id", h.ContextID).
		Str("task_id", h.TaskID).Str("state", string(h.State)).Msg("team: handoff")
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
