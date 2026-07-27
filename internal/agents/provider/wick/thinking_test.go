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
		{"/thinking 4096", true, "4096"},
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

// /thinking off then on restores the session's configured baseline effort.
func TestThinkingToggleRestoresBaseline(t *testing.T) {
	eng, _ := newTestEngine(&fakeModel{}, nil)
	eng.setReasoning(&ReasoningConfig{Effort: "high"}) // configured baseline

	eng.runThinkingCommand("off")
	if eng.reasoning != nil {
		t.Fatalf("after /thinking off, reasoning should be nil, got %+v", eng.reasoning)
	}
	// Gemini channel must also be disabled (budget 0).
	if tc := eng.genCfg.ThinkingConfig; tc == nil || tc.ThinkingBudget == nil || *tc.ThinkingBudget != 0 {
		t.Fatalf("off should zero the Gemini thinking budget, got %+v", eng.genCfg.ThinkingConfig)
	}

	eng.runThinkingCommand("on")
	if eng.reasoning == nil || eng.reasoning.Effort != "high" {
		t.Fatalf("after /thinking on, reasoning should restore baseline effort high, got %+v", eng.reasoning)
	}
}

// A bare /thinking flips the current state.
func TestThinkingBareToggle(t *testing.T) {
	eng, _ := newTestEngine(&fakeModel{}, nil)
	eng.setReasoning(nil) // starts off

	eng.runThinkingCommand("") // → on (default effort)
	if eng.reasoning == nil || eng.reasoning.Effort != thinkingDefaultEffort {
		t.Fatalf("bare toggle from off should turn on at default effort, got %+v", eng.reasoning)
	}
	eng.runThinkingCommand("") // → off
	if eng.reasoning != nil {
		t.Fatalf("bare toggle from on should turn off, got %+v", eng.reasoning)
	}
}

// Explicit effort sets it and enables the Gemini budget.
func TestThinkingSetEffort(t *testing.T) {
	eng, _ := newTestEngine(&fakeModel{}, nil)
	eng.runThinkingCommand("low")
	if eng.reasoning == nil || eng.reasoning.Effort != "low" {
		t.Fatalf("effort not set: %+v", eng.reasoning)
	}
	if tc := eng.genCfg.ThinkingConfig; tc == nil || !tc.IncludeThoughts || tc.ThinkingBudget == nil || *tc.ThinkingBudget != int32(geminiThinkingBudgetForEffort("low")) {
		t.Fatalf("Gemini budget not set for low effort: %+v", eng.genCfg.ThinkingConfig)
	}
}

// runTurn intercepts /thinking: it toggles reasoning and emits a confirmation,
// WITHOUT calling the model (fakeModel.calls stays 0).
func TestRunTurnInterceptsThinking(t *testing.T) {
	f := &fakeModel{}
	eng, lines := newTestEngine(f, nil)
	eng.start()
	eng.runTurn(context.Background(), "/thinking high")

	if f.calls != 0 {
		t.Errorf("/thinking must not reach the model, but GenerateContent ran %d times", f.calls)
	}
	if eng.reasoning == nil || eng.reasoning.Effort != "high" {
		t.Errorf("reasoning not toggled by runTurn: %+v", eng.reasoning)
	}
	joined := strings.Join(*lines, "\n")
	if !strings.Contains(joined, "Thinking ON") {
		t.Errorf("no confirmation emitted; lines=%s", joined)
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
