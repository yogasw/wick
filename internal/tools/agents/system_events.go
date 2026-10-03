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
