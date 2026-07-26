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

	// If a compaction sidecar exists, the turns it covers must NOT be
	// replayed verbatim — they're represented by the summary. Replay
	// [summary note] + only the turns AFTER the cutoff. This is what makes
	// compaction actually stick across a respawn: without it, loadHistory
	// would re-read the whole conversation.jsonl (the full, uncompacted
	// history) and the summary would be pointless. rawIdx tracks the on-disk
	// turn position so we can skip everything at/below CoveredThrough.
	cs := readCompactionState(sessionDir)

	path := filepath.Join(sessionDir, "conversation.jsonl")
	var contents []*genai.Content
	if cs != nil {
		contents = append(contents, genai.NewContentFromText(
			compactionMarker+"\n"+cs.Summary, genai.RoleModel))
	}
	rawIdx := 0
	_ = storage.ReadJSONL(path, func(line []byte) bool {
		idx := rawIdx
		rawIdx++
		var t store.ConversationTurn
		if json.Unmarshal(line, &t) != nil {
			return true
		}
		// Skip turns already folded into the summary.
		if cs != nil && idx < cs.CoveredThrough {
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
		// (wick's own compaction now uses the compaction.json sidecar, not a
		// kind:"compaction" turn — this branch stays for cross-provider
		// sessions that may carry one, kept on the same marker for
		// consistency with the pinning in trimToBudget.)
		if t.Kind == "compaction" && text != "" {
			return genai.NewContentFromText(compactionMarker+"\n"+text, genai.RoleModel)
		}
		return nil
	}
	return nil
}

// trimToBudget drops oldest contents until the estimated token count is
// within budget, always keeping the most recent turns. A single
// oversized most-recent turn is kept as-is (better a big last turn than
// an empty history).
//
// A leading compaction summary note is PINNED: it represents every turn
// already folded away, so dropping it would silently lose all of that
// context. When present we trim the turns after it instead, and keep the
// note at the head.
func trimToBudget(contents []*genai.Content, maxTokens int) []*genai.Content {
	if len(contents) > 0 && isCompactionNote(contents[0]) {
		note := contents[0]
		rest := contents[1:]
		for len(rest) > 1 && estimateTokens(append([]*genai.Content{note}, rest...)) > maxTokens {
			rest = rest[1:]
		}
		return append([]*genai.Content{note}, rest...)
	}
	for len(contents) > 1 && estimateTokens(contents) > maxTokens {
		contents = contents[1:]
	}
	return contents
}

// isCompactionNote reports whether c is a compaction summary note (the
// pinned head produced by compaction / loaded from the sidecar).
func isCompactionNote(c *genai.Content) bool {
	if c == nil || len(c.Parts) == 0 || c.Parts[0] == nil {
		return false
	}
	return strings.HasPrefix(c.Parts[0].Text, compactionMarker)
}

// estimateTokens is the cheap chars/4 heuristic over ALL part payloads —
// not just text. A wick turn's biggest contributors are tool traffic:
// FunctionCall.Args (the params JSON) and especially FunctionResponse
// (a shell/connector result can be tens of KB). Counting only p.Text
// (the old bug) undercounted history by the entire tool-result mass, so
// the context budget was compared against a fraction of the real prompt
// and overflowed the vendor's window (e.g. 557k tokens sent under a 500k
// cap). partChars folds every payload in.
func estimateTokens(contents []*genai.Content) int {
	chars := 0
	for _, c := range contents {
		if c == nil {
			continue
		}
		for _, p := range c.Parts {
			chars += partChars(p)
		}
	}
	return chars / 4
}

// partChars approximates the serialized size of one part: text, plus tool
// call/result payloads which carry no p.Text but dominate real token
// usage. Marshal errors fall back to 0 for that field (can't happen for
// these shapes, but never panic in a size estimator).
func partChars(p *genai.Part) int {
	if p == nil {
		return 0
	}
	n := len(p.Text)
	if fc := p.FunctionCall; fc != nil {
		n += len(fc.Name)
		if b, err := json.Marshal(fc.Args); err == nil {
			n += len(b)
		}
	}
	if fr := p.FunctionResponse; fr != nil {
		n += len(fr.Name)
		if b, err := json.Marshal(fr.Response); err == nil {
			n += len(b)
		}
	}
	return n
}
