package agents

import (
	"encoding/json"
	"strings"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
)

// loadFirstUserMessage returns the cached label from meta (in-memory, fast).
// It never touches disk: list endpoints call this for every session, so a
// per-session jsonl scan here would block the whole list. Sessions whose label
// is still empty fall back to their ID; the label gets backfilled lazily the
// next time the conversation is opened (see resolveLabelFromTurns).
func loadFirstUserMessage(layout agentconfig.Layout, sessionID string, maxLen int) string {
	if globalMgr != nil {
		if sess, ok := globalMgr.Registry().Session(sessionID); ok && sess.Meta.Label != "" {
			return truncateRunes(sess.Meta.Label, maxLen)
		}
	}
	return sessionID
}

// truncateRunes shortens s to maxLen runes, appending "…" when truncated.
func truncateRunes(s string, maxLen int) string {
	r := []rune(s)
	if len(r) > maxLen {
		return string(r[:maxLen]) + "…"
	}
	return s
}

// resolveLabelFromTurns derives a session label from already-loaded turns and
// backfills it to meta.json once, so list endpoints can use the fast path next
// time. Called from the conversation path where turns are read anyway — no
// extra disk scan. Falls back to "(no text)" so empty sessions stop retrying.
func resolveLabelFromTurns(layout agentconfig.Layout, sessionID string, turns []store.ConversationTurn) {
	if globalMgr == nil {
		return
	}
	sess, ok := globalMgr.Registry().Session(sessionID)
	if !ok || sess.Meta.Label != "" {
		return
	}
	label := "(no text)"
	for _, t := range turns {
		if t.Role == "user" && strings.TrimSpace(t.Text) != "" {
			label = strings.TrimSpace(t.Text)
			break
		}
	}
	sess.Meta.Label = label
	_ = session.SaveMeta(layout, sessionID, sess.Meta)
}

// loadConversation reads all ConversationTurn entries from conversation.jsonl
// for the given session. Returns an empty slice when the file is missing.
func loadConversation(layout agentconfig.Layout, sessionID string) ([]store.ConversationTurn, error) {
	var turns []store.ConversationTurn
	err := storage.ReadJSONL(layout.SessionConversation(sessionID), func(line []byte) bool {
		var t store.ConversationTurn
		if json.Unmarshal(line, &t) == nil && t.Role != "" {
			turns = append(turns, t)
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	return turns, nil
}
