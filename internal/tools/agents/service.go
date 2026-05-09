package agents

import (
	"encoding/json"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
)

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

// loadCommands reads raw lines from commands.jsonl for the given session.
func loadCommands(layout agentconfig.Layout, sessionID string) ([]string, error) {
	var lines []string
	err := storage.ReadJSONL(layout.SessionCommands(sessionID), func(line []byte) bool {
		lines = append(lines, string(line))
		return true
	})
	if err != nil {
		return nil, err
	}
	return lines, nil
}
