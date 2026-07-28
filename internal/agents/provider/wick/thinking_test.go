package wick

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/genai"
)

func TestParseThinkingCommand(t *testing.T) {
	cases := []struct {
		in      string
		matched bool
		arg     string
	}{
		{"/thinking", true, ""},
		{"/thinking on", true, "on"},
		{"/thinking OFF", true, "off"},
		{"/thinking High", true, "high"},
		{"  /thinking   low  ", true, "low"},
		{"[system] reap notice\n/thinking on", true, "on"}, // buffered prefix tolerated
		{"can you explain what /thinking does", false, ""}, // not the command line
		{"here is a note\n/thinking on", false, ""},        // real user prefix → don't hijack
		{"/thinkinger", false, ""},                         // not exactly /thinking
		{"", false, ""},
	}
	for _, c := range cases {
		ok, arg := parseThinkingCommand(c.in)
		if ok != c.matched || arg != c.arg {
			t.Errorf("parseThinkingCommand(%q) = (%v,%q), want (%v,%q)", c.in, ok, arg, c.matched, c.arg)
		}
	}
}

// A /thinking command now writes a per-session runtime override; the engine's
// effectiveReasoning() resolves it (override wins over the configured
// baseline). Each test uses a distinct session id so the package-level override
// map doesn't bleed between tests.
func thinkingEngine(t *testing.T, sid string) *engine {
	t.Helper()
	eng, _ := newTestEngine(&fakeModel{}, nil)
	eng.setWickSessionID(sid)
	t.Cleanup(func() { ClearSessionOverride(sid) })
	return eng
}

// /thinking off then on restores the session's configured baseline effort.
func TestThinkingToggleRestoresBaseline(t *testing.T) {
	eng := thinkingEngine(t, "sess-restore")
	eng.setReasoning(&ReasoningConfig{Effort: "high"}) // configured baseline

	eng.runThinkingCommand("off")
	if r := eng.effectiveReasoning(); r != nil {
		t.Fatalf("after /thinking off, effective reasoning should be nil, got %+v", r)
	}

	eng.runThinkingCommand("on")
	if r := eng.effectiveReasoning(); r == nil || r.Effort != "high" {
		t.Fatalf("after /thinking on, reasoning should restore baseline effort high, got %+v", r)
	}
}

// A bare /thinking flips the current state.
func TestThinkingBareToggle(t *testing.T) {
	eng := thinkingEngine(t, "sess-bare")
	eng.setReasoning(nil) // starts off

	eng.runThinkingCommand("") // → on (default effort)
	if r := eng.effectiveReasoning(); r == nil || r.Effort != thinkingDefaultEffort {
		t.Fatalf("bare toggle from off should turn on at default effort, got %+v", r)
	}
	eng.runThinkingCommand("") // → off
	if r := eng.effectiveReasoning(); r != nil {
		t.Fatalf("bare toggle from on should turn off, got %+v", r)
	}
}

// Explicit effort sets it, and applyGeminiThinking mirrors it onto the config.
func TestThinkingSetEffort(t *testing.T) {
	eng := thinkingEngine(t, "sess-effort")
	eng.runThinkingCommand("low")
	r := eng.effectiveReasoning()
	if r == nil || r.Effort != "low" {
		t.Fatalf("effort not set: %+v", r)
	}
	cfg := &genai.GenerateContentConfig{}
	applyGeminiThinking(cfg, "gemini-2.5-flash", r)
	if tc := cfg.ThinkingConfig; tc == nil || !tc.IncludeThoughts || tc.ThinkingBudget == nil || *tc.ThinkingBudget != int32(geminiThinkingBudgetForEffort("low")) {
		t.Fatalf("Gemini budget not mirrored for low effort: %+v", cfg.ThinkingConfig)
	}
}

// Thinking-off must be encoded per model generation. Gemini 2.x takes an
// explicit zero budget; 3.x rejects a zero budget outright (400
// INVALID_ARGUMENT, empty Details) because it is thinking-always, so the config
// must carry no ThinkingConfig at all.
func TestApplyGeminiThinkingOffPerGeneration(t *testing.T) {
	cases := []struct {
		model      string
		wantZeroed bool // true → explicit budget 0; false → ThinkingConfig omitted
	}{
		{"gemini-2.5-flash", true},
		{"gemini-2.0-flash", true},
		{"models/gemini-2.5-pro", true},
		{"gemini-3.5-flash-lite", false},
		{"gemini-3.6-flash", false},
		{"models/gemini-3-pro-preview", false},
		{"some-future-model", false}, // unknown → omit (safe default)
	}
	for _, c := range cases {
		cfg := &genai.GenerateContentConfig{}
		applyGeminiThinking(cfg, c.model, nil)
		if c.wantZeroed {
			tc := cfg.ThinkingConfig
			if tc == nil || tc.ThinkingBudget == nil || *tc.ThinkingBudget != 0 || tc.IncludeThoughts {
				t.Errorf("%s: want explicit zero budget, got %+v", c.model, tc)
			}
			continue
		}
		if cfg.ThinkingConfig != nil {
			t.Errorf("%s: want ThinkingConfig omitted (zero budget is a 400), got %+v",
				c.model, cfg.ThinkingConfig)
		}
	}
}

// A stale zero budget left on the config by applyGenConfig must be cleared for
// 3.x — otherwise the merged config still ships thinkingBudget=0 and 400s.
func TestApplyGeminiThinkingClearsStaleZeroBudget(t *testing.T) {
	zero := int32(0)
	cfg := &genai.GenerateContentConfig{
		ThinkingConfig: &genai.ThinkingConfig{ThinkingBudget: &zero},
	}
	applyGeminiThinking(cfg, "gemini-3.5-flash-lite", nil)
	if cfg.ThinkingConfig != nil {
		t.Fatalf("stale zero budget not cleared for 3.x: %+v", cfg.ThinkingConfig)
	}
}

// runTurn intercepts /thinking: it sets the override and emits a confirmation,
// WITHOUT calling the model (fakeModel.calls stays 0).
func TestRunTurnInterceptsThinking(t *testing.T) {
	f := &fakeModel{}
	eng, lines := newTestEngine(f, nil)
	eng.setWickSessionID("sess-intercept")
	t.Cleanup(func() { ClearSessionOverride("sess-intercept") })
	eng.start()
	eng.runTurn(context.Background(), "/thinking high")

	if f.calls != 0 {
		t.Errorf("/thinking must not reach the model, but GenerateContent ran %d times", f.calls)
	}
	if r := eng.effectiveReasoning(); r == nil || r.Effort != "high" {
		t.Errorf("reasoning not toggled by runTurn: %+v", r)
	}
	joined := strings.Join(*lines, "\n")
	if !strings.Contains(joined, "Thinking ON") {
		t.Errorf("no confirmation emitted; lines=%s", joined)
	}
}

// The generic session-override store is the single source of truth shared with
// the popover endpoint: SetSessionOverride(key,value) is what effectiveReasoning
// reads (the same store the popover POSTs through, one field at a time).
func TestThinkingOverrideStore(t *testing.T) {
	sid := "sess-store"
	t.Cleanup(func() { ClearSessionOverride(sid) })
	eng := thinkingEngine(t, sid)
	eng.setReasoning(&ReasoningConfig{Effort: "medium"}) // baseline

	// Popover turns thinking on at high effort (two field saves).
	SetSessionOverride(sid, "thinking_on", "true")
	SetSessionOverride(sid, "reasoning_effort", "high")
	if r := eng.effectiveReasoning(); r == nil || r.Effort != "high" {
		t.Fatalf("override On should win over baseline: %+v", r)
	}

	// Popover turns it off.
	SetSessionOverride(sid, "thinking_on", "false")
	if r := eng.effectiveReasoning(); r != nil {
		t.Fatalf("override Off should disable: %+v", r)
	}

	// Cleared → back to the configured baseline.
	ClearSessionOverride(sid)
	if r := eng.effectiveReasoning(); r == nil || r.Effort != "medium" {
		t.Fatalf("cleared override should fall back to baseline: %+v", r)
	}
}

// Popover toggle-ON without touching the effort field must PRESERVE the
// configured baseline effort — NOT silently force the schema's default=medium.
// (Regression: overrideReasoning read the tag-defaulted struct instead of the
// raw saved values, so a baseline of "high" was downgraded to "medium".)
func TestThinkingOnPreservesBaselineEffort(t *testing.T) {
	sid := "sess-preserve"
	t.Cleanup(func() { ClearSessionOverride(sid) })
	eng := thinkingEngine(t, sid)
	eng.setReasoning(&ReasoningConfig{Effort: "high"}) // baseline high

	// Only the toggle is saved (no reasoning_effort) — exactly what the popover
	// writes when you flip the switch without touching the dropdown.
	SetSessionOverride(sid, "thinking_on", "true")
	if r := eng.effectiveReasoning(); r == nil || r.Effort != "high" {
		t.Fatalf("toggle-on w/o effort should keep baseline high, got %+v", r)
	}

	// Now the user DOES pick an effort → that wins.
	SetSessionOverride(sid, "reasoning_effort", "low")
	if r := eng.effectiveReasoning(); r == nil || r.Effort != "low" {
		t.Fatalf("explicit effort should win, got %+v", r)
	}
}

// The wick override schema is derived from the wick-tagged struct — the same
// spec the popover renders. Locks the field keys the FE + endpoint rely on.
func TestSessionOverrideSchema(t *testing.T) {
	schema := SessionOverrideSchema("wick")
	byKey := map[string]bool{}
	for _, f := range schema {
		byKey[f.Key] = true
	}
	if !byKey["thinking_on"] || !byKey["reasoning_effort"] {
		t.Fatalf("wick override schema missing expected keys: %+v", schema)
	}
	// The effort field is a dropdown gated on thinking_on.
	for _, f := range schema {
		if f.Key == "reasoning_effort" {
			if f.Type != "dropdown" || f.Options != "low|medium|high" {
				t.Errorf("reasoning_effort widget wrong: type=%q opts=%q", f.Type, f.Options)
			}
			if f.VisibleWhen != "thinking_on:true" {
				t.Errorf("reasoning_effort should be gated on thinking_on:true, got %q", f.VisibleWhen)
			}
		}
	}
	if SessionOverrideSchema("claude") != nil {
		t.Errorf("claude registered no overrides; schema should be nil")
	}
}

func TestGeminiThinkingBudgetForEffort(t *testing.T) {
	if geminiThinkingBudgetForEffort("low") != 2048 ||
		geminiThinkingBudgetForEffort("high") != 16384 ||
		geminiThinkingBudgetForEffort("medium") != 8192 ||
		geminiThinkingBudgetForEffort("") != 8192 {
		t.Fatal("effort→budget tiers wrong")
	}
	_ = genai.ThinkingConfig{}
}
