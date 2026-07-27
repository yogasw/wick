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
//	/thinking <N>        → enable with an explicit token budget
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

// runThinkingCommand applies a /thinking command to the live engine state and
// reports the result. It flips BOTH channels reasoning travels through:
// e.reasoning (read by the OpenAI/Anthropic adapters) and the Gemini
// ThinkingConfig on e.genCfg — so the toggle is honored whatever the model is.
// The change persists for the rest of the session (until another /thinking).
func (e *engine) runThinkingCommand(arg string) {
	var msg string
	switch arg {
	case "off", "no", "false", "0":
		e.applyReasoning(nil)
		msg = "🧠 Thinking OFF — the model will answer without extended reasoning."
	case "", "toggle":
		// Bare /thinking flips the current state.
		if e.reasoning != nil {
			e.applyReasoning(nil)
			msg = "🧠 Thinking OFF — the model will answer without extended reasoning."
		} else {
			r := e.reasoningForOn()
			e.applyReasoning(r)
			msg = "🧠 Thinking ON" + effortSuffix(r) + "."
		}
	case "on", "yes", "true", "1":
		r := e.reasoningForOn()
		e.applyReasoning(r)
		msg = "🧠 Thinking ON" + effortSuffix(r) + "."
	case "low", "medium", "high":
		r := &ReasoningConfig{Effort: arg}
		e.applyReasoning(r)
		msg = "🧠 Thinking ON" + effortSuffix(r) + "."
	default:
		if n := atoiSafe(arg); n > 0 {
			r := &ReasoningConfig{BudgetTokens: n}
			e.applyReasoning(r)
			msg = fmt.Sprintf("🧠 Thinking ON (budget %d tokens).", n)
		} else {
			msg = "Usage: /thinking [on|off|low|medium|high|<budget-tokens>] — no argument toggles."
		}
	}
	// Report as an assistant text turn so it lands in the transcript and the
	// user sees the confirmation; no model call.
	e.emit(textLine(msg))
	e.emit(doneLine(msg))
}

// reasoningForOn returns the request /thinking on should enable: the session's
// configured baseline if there was one, else a default-effort request.
func (e *engine) reasoningForOn() *ReasoningConfig {
	if e.defaultReasoning != nil {
		return e.defaultReasoning
	}
	return &ReasoningConfig{Effort: thinkingDefaultEffort}
}

// applyReasoning sets the live reasoning request AND mirrors it onto the Gemini
// ThinkingConfig (the SDK adapter reads that, not e.reasoning). nil disables
// both.
func (e *engine) applyReasoning(r *ReasoningConfig) {
	e.reasoning = r
	if e.genCfg == nil {
		e.genCfg = &genai.GenerateContentConfig{}
	}
	if r == nil {
		// Explicit zero budget disables Gemini thinking.
		zero := int32(0)
		e.genCfg.ThinkingConfig = &genai.ThinkingConfig{IncludeThoughts: false, ThinkingBudget: &zero}
		return
	}
	budget := int32(r.BudgetTokens)
	if budget <= 0 {
		budget = int32(geminiThinkingBudgetForEffort(r.Effort))
	}
	e.genCfg.ThinkingConfig = &genai.ThinkingConfig{IncludeThoughts: true, ThinkingBudget: &budget}
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

// atoiSafe parses a positive integer, returning 0 on any non-numeric input.
func atoiSafe(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int(c-'0')
	}
	return n
}
