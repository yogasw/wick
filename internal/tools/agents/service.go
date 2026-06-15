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
// Falls back to scanning conversation.jsonl for sessions created before label
// caching was introduced.
func loadFirstUserMessage(layout agentconfig.Layout, sessionID string, maxLen int) string {
	if globalMgr != nil {
		if sess, ok := globalMgr.Registry().Session(sessionID); ok && sess.Meta.Label != "" {
			r := []rune(sess.Meta.Label)
			if len(r) > maxLen {
				return string(r[:maxLen]) + "…"
			}
			return sess.Meta.Label
		}
	}
	// Legacy fallback: scan conversation.jsonl and backfill label.
	var result string
	storage.ReadJSONL(layout.SessionConversation(sessionID), func(line []byte) bool {
		var t store.ConversationTurn
		if json.Unmarshal(line, &t) != nil || t.Role != "user" || t.Text == "" {
			return true
		}
		r := []rune(strings.TrimSpace(t.Text))
		if len(r) > maxLen {
			result = string(r[:maxLen]) + "…"
		} else {
			result = string(r)
		}
		return false
	})
	// Backfill label to disk so next call hits the fast path.
	if result != "" && globalMgr != nil {
		if sess, ok := globalMgr.Registry().Session(sessionID); ok && sess.Meta.Label == "" {
			sess.Meta.Label = result
			_ = session.SaveMeta(layout, sessionID, sess.Meta)
		}
	}
	return result
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
