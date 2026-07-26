package wick

import (
	"context"
	"fmt"
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
// into a single summary each normal (threshold-triggered) compaction pass.
const compactOldestFraction = 0.5

// compactAggressiveFraction is used by the overflow-recovery path: the
// request already exceeded the vendor window, so fold a bigger slice to
// claw back more headroom in one pass.
const compactAggressiveFraction = 0.7

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
// compaction (unbounded). Safe to call before every turn AND inside the
// tool-call loop (history balloons as tool results append).
func (e *engine) maybeCompact(ctx context.Context, budget int) bool {
	if budget <= 0 || len(e.history) < 4 {
		return false
	}
	if e.calibratedEstimate(e.history) < int(float64(budget)*compactionTriggerRatio) {
		return false
	}
	return e.compactBy(ctx, compactOldestFraction)
}

// compactAggressively folds a larger oldest slice unconditionally (no
// threshold check) — the overflow-recovery path where the request already
// exceeded the window. Returns false when there's nothing left to fold
// (history is already just the recent tail + a prior summary), signalling
// the caller that summarising more won't shrink the request.
func (e *engine) compactAggressively(ctx context.Context) bool {
	if len(e.history) < 4 {
		return false
	}
	return e.compactBy(ctx, compactAggressiveFraction)
}

// isCompactCommand reports whether userText is the /compact command,
// tolerating a buffered non-user turn the pool may prepend. Accepts:
//   - exactly "/compact"
//   - a buffered "[system] …" block followed by a "/compact" line
// It requires the last non-empty line to be exactly "/compact" AND every
// line before it (if any) to be part of a "[system]" buffered notice — so
// a user whose real message merely ends in "/compact" isn't hijacked.
func isCompactCommand(userText string) bool {
	t := strings.TrimSpace(userText)
	if t == "/compact" {
		return true
	}
	if !strings.HasSuffix(t, "/compact") {
		return false
	}
	lines := strings.Split(t, "\n")
	// Last non-empty line must be exactly "/compact".
	last := ""
	lastIdx := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			last = strings.TrimSpace(lines[i])
			lastIdx = i
			break
		}
	}
	if last != "/compact" {
		return false
	}
	// Everything above it must be a buffered [system] notice (context-only),
	// not a genuine user message that happens to end with /compact.
	for i := 0; i < lastIdx; i++ {
		s := strings.TrimSpace(lines[i])
		if s == "" {
			continue
		}
		if !strings.HasPrefix(s, "[system]") {
			return false
		}
	}
	return true
}

// runManualCompact handles the /compact command: force a compaction now
// (ignoring the budget threshold) and report the outcome as a normal
// assistant turn so the user sees confirmation in the chat. It emits its
// own done line, so runTurn returns straight after calling this.
func (e *engine) runManualCompact(ctx context.Context) {
	if len(e.history) < 4 {
		e.emit(textLine("Nothing to compact yet — the conversation is still short."))
		e.emit(doneLine(""))
		return
	}
	before := estimateTokens(e.history)
	beforeTurns := len(e.history)
	if !e.compactAggressively(ctx) {
		e.emit(textLine("Couldn't compact further — the recent turns already fit and there's no older history left to summarize."))
		e.emit(doneLine(""))
		return
	}
	after := estimateTokens(e.history)
	title := fmt.Sprintf("Context compacted — folded %d earlier turns (~%d → ~%d tokens)", beforeTurns-len(e.history), before, after)
	// Emit as a `detail` fence: a small chip in the chat (title) that opens
	// the full summary in a modal on click, instead of dumping the whole
	// summary into the transcript. The chip degrades to plain text on
	// non-rich channels. See fe common-md `detail` fence.
	summary := currentSummaryText(e)
	e.emit(textLine(detailFence(title, summary)))
	e.emit(doneLine(title))
}

// currentSummaryText returns the summary now at the head of history (the
// note compaction just wrote), stripped of the marker prefix — the body
// shown in the /compact detail modal. Empty when the head isn't a note.
func currentSummaryText(e *engine) string {
	if len(e.history) == 0 || len(e.history[0].Parts) == 0 || e.history[0].Parts[0] == nil {
		return ""
	}
	txt := e.history[0].Parts[0].Text
	return strings.TrimSpace(strings.TrimPrefix(txt, compactionMarker))
}

// detailFence wraps a title + body in a ```detail fence the FE renders as a
// clickable chip → modal. Body line 1 is the chip title.
func detailFence(title, body string) string {
	return "```detail\n" + title + "\n" + body + "\n```"
}

// compactBy folds the oldest `fraction` of history into one model-authored
// summary note, in place. Returns true when it actually shrank history.
// The cut is advanced to a user-role boundary so a kept tool_result never
// dangles without its preceding tool_call (which breaks vendors).
func (e *engine) compactBy(ctx context.Context, fraction float64) bool {
	cut := int(float64(len(e.history)) * fraction)
	if cut < 1 {
		return false
	}
	for cut < len(e.history) && e.history[cut].Role != genai.RoleUser {
		cut++
	}
	if cut >= len(e.history) {
		return false
	}

	old := e.history[:cut]
	// Compaction is an extra LLM call that can take a few seconds. Signal it
	// to the UI (a thinking line, which the parser surfaces) so the session
	// reads as "compacting…", not stalled — covers every trigger: auto
	// (mid-turn budget), overflow-recovery, and /compact. emit is always
	// wired on a real spawn; guard for the test engine that leaves it nil.
	if e.emit != nil {
		e.emit(thinkingLine("Compacting earlier context to free up room…"))
	}
	summary, err := e.summarize(ctx, old)
	if err != nil || strings.TrimSpace(summary) == "" {
		log.Warn().Err(err).Msg("wick.compact: summarize failed, keeping full history")
		return false
	}

	// Replace the oldest slice with one model-authored summary note. The
	// marker line is explicit ("everything above is compacted") so both the
	// model and a human reading the trace know this is a boundary, not real
	// dialogue.
	note := genai.NewContentFromText(compactionMarker+"\n"+summary, genai.RoleModel)
	kept := append([]*genai.Content{note}, e.history[cut:]...)
	log.Info().
		Int("before", len(e.history)).
		Int("after", len(kept)).
		Int("summarized_turns", cut).
		Msg("wick.compact: history compacted")
	e.history = kept

	// Persist the summary to the sidecar so it survives a respawn. The
	// cutoff is the current conversation.jsonl turn count: every settled
	// turn on disk is subsumed by this summary (which itself folds in any
	// prior summary), and turns that arrive afterwards sit past the cutoff
	// and replay normally. Counting mid-turn undercounts at worst, which is
	// safe — a not-yet-counted turn just replays again rather than being
	// dropped. Best-effort: an unwritable sidecar only costs a re-compact
	// next spawn.
	if e.toolSpillDir != "" {
		writeCompactionState(e.toolSpillDir, compactionState{
			Summary:        summary,
			CoveredThrough: countConversationTurns(e.toolSpillDir),
		})
	}
	return true
}

// compactionMarker heads the in-history summary note AND the loaded-from-
// sidecar note, so there is one recognizable boundary string everywhere.
const compactionMarker = "[Earlier conversation compacted — everything above this point is summarized below, not verbatim history.]"

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
