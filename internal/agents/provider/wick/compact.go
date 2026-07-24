package wick

import (
	"context"
	"strings"

	"github.com/rs/zerolog/log"
	"google.golang.org/genai"
)

// compact.go implements model-driven context compaction — wick's
// equivalent of Claude's /compact. When the replayed history nears the
// context budget, wick asks the MODEL to summarize the oldest turns
// (preserving decisions, facts, open tasks, file paths), then replaces
// those turns with the summary. This is what keeps long / complex
// multi-step tasks alive without blowing the model's context window —
// unlike a dumb newest-first trim, the older context is preserved in
// compressed form rather than dropped.
//
// v1 is in-memory: it compacts the engine's live history slice for the
// duration of the spawn. Persisting the summary as a kind:"compaction"
// turn in conversation.jsonl (so it survives a respawn) is a documented
// later enhancement — a respawn simply re-compacts, costing one extra
// model call.

// compactionTriggerRatio fires compaction when the estimated replay
// exceeds this fraction of the budget. 0.8 leaves headroom for the
// current turn + the model's output.
const compactionTriggerRatio = 0.8

// compactOldestFraction is the portion of history (oldest end) folded
// into a single summary each compaction pass.
const compactOldestFraction = 0.5

// compactionPrompt instructs the model to produce a dense, structured
// summary optimised for continuing the task, not prose.
const compactionPrompt = `Summarize the conversation so far into a compact briefing that lets you continue the task with no loss of essential context. Preserve:
- decisions made and why
- concrete facts, values, identifiers, file paths, commands that matter
- what has been done vs what is still pending
- any user preferences or constraints stated
Omit chit-chat and redundant detail. Output the summary only, no preamble.`

// maybeCompact compacts eng.history in place when it exceeds the budget
// threshold. Returns true when a compaction happened. budget<=0 disables
// compaction (unbounded). Safe to call before every turn.
func (e *engine) maybeCompact(ctx context.Context, budget int) bool {
	if budget <= 0 || len(e.history) < 4 {
		return false
	}
	if estimateTokens(e.history) < int(float64(budget)*compactionTriggerRatio) {
		return false
	}

	cut := int(float64(len(e.history)) * compactOldestFraction)
	if cut < 1 {
		return false
	}
	// Never split a model tool-call from the user tool-result that answers
	// it: advance the cut to a user-role boundary so the kept tail starts
	// clean (a dangling tool_result with no preceding call breaks vendors).
	for cut < len(e.history) && e.history[cut].Role != genai.RoleUser {
		cut++
	}
	if cut >= len(e.history) {
		return false
	}

	old := e.history[:cut]
	summary, err := e.summarize(ctx, old)
	if err != nil || strings.TrimSpace(summary) == "" {
		log.Warn().Err(err).Msg("wick.compact: summarize failed, keeping full history")
		return false
	}

	// Replace the oldest slice with one model-authored summary note.
	note := genai.NewContentFromText("[Summary of earlier conversation]\n"+summary, genai.RoleModel)
	kept := append([]*genai.Content{note}, e.history[cut:]...)
	log.Info().
		Int("before", len(e.history)).
		Int("after", len(kept)).
		Int("summarized_turns", cut).
		Msg("wick.compact: history compacted")
	e.history = kept
	return true
}

// summarize asks the model to summarize the given turns. It runs a
// separate, tool-less generation (no history mutation, no tools) so the
// summary can't trigger tool calls or recurse into compaction.
func (e *engine) summarize(ctx context.Context, turns []*genai.Content) (string, error) {
	contents := make([]*genai.Content, 0, len(turns)+1)
	contents = append(contents, turns...)
	contents = append(contents, genai.NewContentFromText(compactionPrompt, genai.RoleUser))

	req := &LLMRequest{
		Model:    e.modelName,
		Contents: contents,
		Config:   &genai.GenerateContentConfig{}, // no system prompt, no tools
	}
	var out *LLMResponse
	for resp, err := range e.llm.GenerateContent(ctx, req, false) {
		if err != nil {
			return "", err
		}
		out = resp
	}
	if out == nil || out.Content == nil {
		return "", nil
	}
	var sb strings.Builder
	for _, p := range out.Content.Parts {
		if p != nil && p.Text != "" && !p.Thought {
			sb.WriteString(p.Text)
		}
	}
	return sb.String(), nil
}
