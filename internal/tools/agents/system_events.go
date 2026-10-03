package agents

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// evSystemEvent is the SSE event every server-recorded system turn is
// pushed as. Data is the turn itself, the same JSON the conversation
// endpoint returns, so a live row and a reloaded one render alike.
const evSystemEvent = "system_event"

// systemTurn builds a server-authored system turn of kind. Only wick
// writes these (an agent's reply is always role=assistant), which is what
// makes a chip in the timeline trustworthy.
func systemTurn(kind, text string, extras map[string]string, now time.Time) store.ConversationTurn {
	now = now.UTC()
	return store.ConversationTurn{
		TurnID:    fmt.Sprintf("%d", now.UnixNano()),
		Timestamp: now,
		Role:      "system",
		Kind:      kind,
		Text:      text,
		Extras:    extras,
	}
}

// recordSystemTurn writes turn into sessionID's thread, pushes it to open
// viewers and marks the session active so the roster shows it unread.
// Best-effort: a failed write is logged, never surfaced to whoever caused
// the event.
func recordSystemTurn(layout agentconfig.Layout, b *Broadcaster, sessionID string, turn store.ConversationTurn) {
	if layout.BaseDir == "" || sessionID == "" {
		return
	}
	if err := storage.AppendJSONL(layout.SessionConversation(sessionID), "wick-conv-v1", sessionID, turn); err != nil {
		log.Warn().Err(err).Str("session", sessionID).Str("kind", turn.Kind).Msg("agents: system event write failed")
		return
	}
	publishSystemTurnEvent(b, sessionID, turn)
	touchSessionActive(layout, sessionID)
}

// publishSystemTurnEvent pushes turn as a system_event. mention_handoff is
// also pushed under its own name, which the thread store listened for
// before system_event existed; both carry the same turn_id.
func publishSystemTurnEvent(b *Broadcaster, sessionID string, turn store.ConversationTurn) {
	if b == nil || sessionID == "" {
		return
	}
	body, err := json.Marshal(turn)
	if err != nil {
		return
	}
	b.PublishRaw(sessionID, "", evSystemEvent, string(body))
	if turn.Kind == store.KindMentionHandoff {
		b.PublishRaw(sessionID, "", store.KindMentionHandoff, string(body))
	}
}

// touchSessionActive moves the session's last activity to now — what the
// roster's unread badge compares against.
func touchSessionActive(layout agentconfig.Layout, sessionID string) {
	sess, err := session.Load(layout, sessionID)
	if err != nil {
		return
	}
	sess.Meta.LastActive = time.Now().UTC()
	if err := session.SaveMeta(layout, sessionID, sess.Meta); err != nil {
		return
	}
	if globalMgr != nil {
		_ = globalMgr.RefreshSession(sessionID)
	}
}

// emitSystemEvent is recordSystemTurn on the process-wide layout and
// broadcaster — the one call every emit site uses.
func emitSystemEvent(sessionID, kind, text string, extras map[string]string) {
	recordSystemTurn(globalLayout, globalBcast, sessionID, systemTurn(kind, text, extras, time.Now()))
}

// grantsSummary renders grants as "Notion (read), Slack (all)" for a chip.
// label maps a connector id to its name; an unknown id shows as the id.
func grantsSummary(grants []team.ConnectorGrant, label func(string) string) string {
	parts := make([]string, 0, len(grants))
	for _, g := range grants {
		if g.Level == team.LevelOff {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", label(g.ConnectorID), grantLevel(g)))
	}
	return strings.Join(parts, ", ")
}

func grantLevel(g team.ConnectorGrant) string {
	switch g.Level {
	case team.LevelAll, team.LevelPick:
		return g.Level
	default:
		return team.LevelRead
	}
}

// grantsDiff lists what moved between two grant sets as "+Notion (read)",
// "-Slack" and "Loki: read → all". Empty when nothing a person would call
// access changed.
func grantsDiff(before, after []team.ConnectorGrant, label func(string) string) []string {
	idx := func(gs []team.ConnectorGrant) map[string]team.ConnectorGrant {
		m := map[string]team.ConnectorGrant{}
		for _, g := range gs {
			if g.Level != team.LevelOff {
				m[g.ConnectorID] = g
			}
		}
		return m
	}
	was, now := idx(before), idx(after)
	var out []string
	for id, g := range now {
		old, ok := was[id]
		switch {
		case !ok:
			out = append(out, fmt.Sprintf("+%s (%s)", label(id), grantLevel(g)))
		case grantLevel(old) != grantLevel(g):
			out = append(out, fmt.Sprintf("%s: %s → %s", label(id), grantLevel(old), grantLevel(g)))
		case !slices.Equal(old.Ops, g.Ops) || !slices.Equal(old.Accounts, g.Accounts):
			out = append(out, fmt.Sprintf("%s: ops/accounts changed", label(id)))
		}
	}
	for id := range was {
		if _, ok := now[id]; !ok {
			out = append(out, "-"+label(id))
		}
	}
	slices.Sort(out)
	return out
}

// actorName is the logged-in user as a chip names them.
func actorName(c *tool.Ctx) string {
	u := login.GetUser(c.Context())
	if u == nil {
		return ""
	}
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}

// connectorLabeler maps a connector id to the label the caller sees; an
// id missing from the caller's catalog shows as itself.
func connectorLabeler(c *tool.Ctx) func(string) string {
	labels := map[string]string{}
	if cat, err := ownerCatalog(c); err == nil {
		for _, e := range cat {
			labels[e.Row.ID] = e.Row.Label
		}
	}
	return func(id string) string {
		if l := labels[id]; l != "" {
			return l
		}
		return id
	}
}

// agentCreatedText is the agent_created chip: "Rekap joined the team ·
// created by Yoga · Notion (read)".
func agentCreatedText(name, createdBy, approvedBy, grants string) string {
	t := name + " joined the team"
	if createdBy != "" {
		t += " · created by " + createdBy
	}
	if approvedBy != "" {
		t += " · approved by " + approvedBy
	}
	if grants != "" {
		t += " · " + grants
	}
	return t
}

// agentCreatedExtras is agent_created's extras. how is where the agent
// came from: "wizard", "convert" or "chat" (its main chat opened later).
func agentCreatedExtras(p entity.AgentPersona, name, createdBy, approvedBy string, label func(string) string, how string) map[string]string {
	return map[string]string{
		"agent_id": p.ID, "handle": p.Handle, "name": name,
		"created_by": createdBy, "approved_by": approvedBy,
		"grants_summary": grantsSummary(team.DecodeGrants(p.AllowedConnectors), label),
		"via":            how,
	}
}

// announceAgentCreated records agent_created in the new agent's main chat
// when it already has one (a converted project), and a copy in the
// Captain's — the owner's lead agent hears of every new teammate. A main
// chat opened later gets its own chip from createTeamAgentSession.
func announceAgentCreated(c *tool.Ctx, p entity.AgentPersona, name, how string) {
	label := connectorLabeler(c)
	who := actorName(c)
	extras := agentCreatedExtras(p, name, who, "", label, how)
	text := agentCreatedText(name, who, "", extras["grants_summary"])
	if s, ok := mainSessionOf(p.OwnerUserID, p.ID); ok {
		emitSystemEvent(s.ID, store.KindAgentCreated, text, extras)
	}
	if p.IsCaptain || globalTeam == nil {
		return
	}
	rows, err := globalTeam.List(c.Context(), p.OwnerUserID)
	if err != nil {
		return
	}
	for _, r := range rows {
		if r.IsCaptain && r.ID != p.ID {
			if s, ok := mainSessionOf(r.OwnerUserID, r.ID); ok {
				emitSystemEvent(s.ID, store.KindAgentCreated, text, extras)
			}
		}
	}
}

// announceAccessChanged records access_changed in the agent's main chat
// when the Settings save moved its connector grants, the include-new
// switch or its run-as identity. Nothing is recorded for a save that
// left access as it was (the drawer autosaves every field).
func announceAccessChanged(c *tool.Ctx, before, after entity.AgentPersona) {
	label := connectorLabeler(c)
	changes := grantsDiff(team.DecodeGrants(before.AllowedConnectors), team.DecodeGrants(after.AllowedConnectors), label)
	if before.IncludeNewConnectors != after.IncludeNewConnectors {
		if after.IncludeNewConnectors {
			changes = append(changes, "+new connectors")
		} else {
			changes = append(changes, "-new connectors")
		}
	}
	if before.RunAs != after.RunAs {
		changes = append(changes, "runs as: "+after.RunAs)
	}
	if len(changes) == 0 {
		return
	}
	s, ok := mainSessionOf(after.OwnerUserID, after.ID)
	if !ok {
		return
	}
	who := actorName(c)
	summary := strings.Join(changes, ", ")
	text := fmt.Sprintf("Access of @%s changed: %s", after.Handle, summary)
	if who != "" {
		text += " · by " + who
	}
	emitSystemEvent(s.ID, store.KindAccessChanged, text, map[string]string{
		"agent_id": after.ID, "handle": after.Handle, "changed_by": who,
		"changes": summary, "grants_summary": grantsSummary(team.DecodeGrants(after.AllowedConnectors), label),
	})
}
