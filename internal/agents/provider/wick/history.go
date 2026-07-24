package wick

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
	"google.golang.org/genai"
)

// loadHistory rebuilds the model message history for a session from its
// conversation.jsonl — the same provider-agnostic store every provider
// writes through the parser pipeline. This is what gives wick
// cross-provider continuity: a session started on claude/codex/gemini
// keeps its conversation when switched to wick, because the turns are
// read from the shared store, not a wick-private file.
//
// v1 replays TEXT turns only (user + assistant). Tool-call trace replay
// (reading thinking/<turn_id>.json) is deferred behind config — richer
// grounding, more tokens. System turns are skipped in v1 except the
// compaction summary, which is folded in as a model note.
//
// maxContextTokens>0 trims oldest-first (keeping the most recent turns)
// so the replay fits the budget. Token count uses the cheap chars/4
// estimate; the engine calibrates against real usage on later turns.
func loadHistory(sessionDir string, maxContextTokens int) []*genai.Content {
	if sessionDir == "" {
		return nil
	}
	path := filepath.Join(sessionDir, "conversation.jsonl")
	var contents []*genai.Content
	_ = storage.ReadJSONL(path, func(line []byte) bool {
		var t store.ConversationTurn
		if json.Unmarshal(line, &t) != nil {
			return true
		}
		if c := turnToContent(t); c != nil {
			contents = append(contents, c)
		}
		return true
	})
	if maxContextTokens > 0 {
		contents = trimToBudget(contents, maxContextTokens)
	}
	return contents
}

// turnToContent maps one stored turn to a genai.Content, or nil to skip.
func turnToContent(t store.ConversationTurn) *genai.Content {
	text := strings.TrimSpace(t.Text)
	switch t.Role {
	case "user":
		if text == "" {
			return nil
		}
		return genai.NewContentFromText(text, genai.RoleUser)
	case "assistant":
		if text == "" {
			return nil
		}
		return genai.NewContentFromText(text, genai.RoleModel)
	case "system":
		// Only the compaction summary carries context worth replaying;
		// other system turns (provider_switch, errors) are display-only.
		if t.Kind == "compaction" && text != "" {
			return genai.NewContentFromText("[earlier conversation summary]\n"+text, genai.RoleModel)
		}
		return nil
	}
	return nil
}

// trimToBudget drops oldest contents until the estimated token count is
// within budget, always keeping the most recent turns. A single
// oversized most-recent turn is kept as-is (better a big last turn than
// an empty history).
func trimToBudget(contents []*genai.Content, maxTokens int) []*genai.Content {
	for len(contents) > 1 && estimateTokens(contents) > maxTokens {
		contents = contents[1:]
	}
	return contents
}

// estimateTokens is the cheap chars/4 heuristic over all text parts.
func estimateTokens(contents []*genai.Content) int {
	chars := 0
	for _, c := range contents {
		if c == nil {
			continue
		}
		for _, p := range c.Parts {
			if p != nil {
				chars += len(p.Text)
			}
		}
	}
	return chars / 4
}
