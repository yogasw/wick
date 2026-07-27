package wick

import (
	"fmt"
	"strings"

	"google.golang.org/genai"
)

// thinking.go implements the /thinking runtime command: a per-session toggle
// for the model's reasoning, mirroring Claude Code's /thinking. It never
// reaches the model as a prompt — runTurn intercepts it, flips the engine's
// live reasoning request, and reports the new state as an assistant line.
//
// Grammar (case-insensitive, arg optional):
//
//	/thinking            → toggle on/off from the current state
//	/thinking on         → enable (restore the configured effort, or default)
//	/thinking off        → disable
//	/thinking low|medium|high  → enable at that effort
//
// Like /compact it tolerates a buffered "[system] …" turn the pool may prepend
// (the real command must be the last non-empty line).

// thinkingDefaultEffort is used when /thinking on has no configured baseline to
// restore (the session never had reasoning set). "medium" is the sensible
// middle tier every effort-speaking vendor accepts.
const thinkingDefaultEffort = "medium"

// parseThinkingCommand reports whether userText is a /thinking command and, if
// so, returns its lowercased argument ("" for a bare toggle). Mirrors
// isCompactCommand's last-non-empty-line + [system]-prefix tolerance so a real
// message that merely mentions /thinking isn't hijacked.
func parseThinkingCommand(userText string) (matched bool, arg string) {
	t := strings.TrimSpace(userText)
	lines := strings.Split(t, "\n")
	last, lastIdx := "", -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			last = strings.TrimSpace(lines[i])
			lastIdx = i
			break
		}
	}
	if !strings.HasPrefix(strings.ToLower(last), "/thinking") {
		return false, ""
	}
	// The word after the slash must be exactly "thinking" (not "/thinkinger").
	fields := strings.Fields(last)
	if len(fields) == 0 || strings.ToLower(fields[0]) != "/thinking" {
		return false, ""
	}
	// Everything above the command line must be a buffered [system] notice, not
	// a genuine user message that happens to end with /thinking.
	for i := 0; i < lastIdx; i++ {
		s := strings.TrimSpace(lines[i])
		if s != "" && !strings.HasPrefix(s, "[system]") {
			return false, ""
		}
	}
	if len(fields) > 1 {
		arg = strings.ToLower(fields[1])
	}
	return true, arg
}

// runThinkingCommand applies a /thinking TEXT command (the manual-typing
// fallback) by writing the SAME per-session override store the popover uses,
// then reports the result. One source of truth: the engine reads the override
// before every model call (effectiveReasoning), so this affects
// OpenAI/Anthropic and Gemini alike. The change lasts the rest of the session
// (until another /thinking or teardown). An effort budget typed as a number is
// converted to the nearest effort tier since the override schema is effort-based.
func (e *engine) runThinkingCommand(arg string) {
	sid := e.wickSessionID
	var msg string
	switch arg {
	case "off", "no", "false", "0":
		setThinkingValues(sid, false, "")
		msg = "🧠 Thinking OFF — the model will answer without extended reasoning."
	case "", "toggle":
		// Bare /thinking flips the CURRENT effective state.
		if e.effectiveReasoning() != nil {
			setThinkingValues(sid, false, "")
			msg = "🧠 Thinking OFF — the model will answer without extended reasoning."
		} else {
			r := e.reasoningForOn()
			setThinkingValues(sid, true, r.Effort)
			msg = "🧠 Thinking ON" + effortSuffix(r) + "."
		}
	case "on", "yes", "true", "1":
		r := e.reasoningForOn()
		setThinkingValues(sid, true, r.Effort)
		msg = "🧠 Thinking ON" + effortSuffix(r) + "."
	case "low", "medium", "high":
		setThinkingValues(sid, true, arg)
		msg = "🧠 Thinking ON (effort: " + arg + ")."
	default:
		msg = "Usage: /thinking [on|off|low|medium|high] — no argument toggles."
	}
	// Report as an assistant text turn so it lands in the transcript and the
	// user sees the confirmation; no model call.
	e.emit(textLine(msg))
	e.emit(doneLine(msg))
}

// effectiveReasoning resolves the reasoning to apply on the next model call: the
// runtime /thinking override (popover or text command) wins over the configured
// baseline. No override saved → the baseline (e.reasoning). Override OFF → nil.
// Override ON without an effort → the baseline-or-default (reasoningForOn).
func (e *engine) effectiveReasoning() *ReasoningConfig {
	r, set := overrideReasoning(e.wickSessionID)
	if !set {
		return e.reasoning // no runtime override → configured baseline
	}
	if r != nil {
		return r // ON with an explicit effort
	}
	// The override is set: either OFF (→ nil) or ON-without-effort. Distinguish
	// by re-reading the flag; ON-without-effort falls back to the baseline/default.
	o := overrideConfig(e.wickSessionID)
	if !o.ThinkingOn {
		return nil
	}
	return e.reasoningForOn()
}

// reasoningForOn returns the request "on" should enable: the session's
// configured baseline if there was one, else a default-effort request.
func (e *engine) reasoningForOn() *ReasoningConfig {
	if e.defaultReasoning != nil {
		return e.defaultReasoning
	}
	return &ReasoningConfig{Effort: thinkingDefaultEffort}
}

// applyGeminiThinking mirrors a resolved reasoning request onto a genai config's
// ThinkingConfig — the channel the Gemini SDK adapter reads (it doesn't see
// LLMRequest.Reasoning). nil disables thinking (explicit zero budget). Applied
// per-call on a cloned config so a runtime toggle is honored without mutating
// the stored baseline.
func applyGeminiThinking(cfg *genai.GenerateContentConfig, r *ReasoningConfig) {
	if cfg == nil {
		return
	}
	if r == nil {
		zero := int32(0)
		cfg.ThinkingConfig = &genai.ThinkingConfig{IncludeThoughts: false, ThinkingBudget: &zero}
		return
	}
	budget := int32(r.BudgetTokens)
	if budget <= 0 {
		budget = int32(geminiThinkingBudgetForEffort(r.Effort))
	}
	cfg.ThinkingConfig = &genai.ThinkingConfig{IncludeThoughts: true, ThinkingBudget: &budget}
}

// geminiThinkingBudgetForEffort maps an effort level to a Gemini thinking
// budget. Gemini takes a token budget (like Anthropic), so /thinking on|high
// picks a concrete number. Mirrors antThinkingBudgetForEffort's tiers.
func geminiThinkingBudgetForEffort(effort string) int {
	switch effort {
	case "low":
		return 2048
	case "high":
		return 16384
	default: // medium / unspecified
		return 8192
	}
}

// effortSuffix renders " (effort: high)" / " (budget N tokens)" / "" for the
// confirmation message.
func effortSuffix(r *ReasoningConfig) string {
	if r == nil {
		return ""
	}
	if r.Effort != "" {
		return " (effort: " + r.Effort + ")"
	}
	if r.BudgetTokens > 0 {
		return fmt.Sprintf(" (budget %d tokens)", r.BudgetTokens)
	}
	return ""
}
