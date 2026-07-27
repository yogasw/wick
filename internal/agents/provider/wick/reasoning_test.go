package wick

import (
	"testing"

	"google.golang.org/genai"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

func ptrInt(n int) *int { return &n }

// buildReasoning: model overrides instance; effort preferred over budget;
// nil when neither is set.
func TestBuildReasoning(t *testing.T) {
	// Neither set → nil.
	if r := buildReasoning(provider.WickModel{}, nil); r != nil {
		t.Fatalf("empty → want nil, got %+v", r)
	}
	// Instance budget, model effort → model effort wins, budget carried too.
	inst := &provider.WickConfig{GenConfig: &provider.WickGenConfig{ThinkingBudget: ptrInt(4000)}}
	m := provider.WickModel{GenConfig: &provider.WickGenConfig{ReasoningEffort: "High"}}
	r := buildReasoning(m, inst)
	if r == nil || r.Effort != "high" {
		t.Fatalf("want effort=high (lowercased), got %+v", r)
	}
	if r.BudgetTokens != 4000 {
		t.Fatalf("want budget carried from instance = 4000, got %d", r.BudgetTokens)
	}
	// Only budget → effort empty.
	r2 := buildReasoning(provider.WickModel{GenConfig: &provider.WickGenConfig{ThinkingBudget: ptrInt(1234)}}, nil)
	if r2 == nil || r2.Effort != "" || r2.BudgetTokens != 1234 {
		t.Fatalf("budget-only → want {,,1234}, got %+v", r2)
	}
}

// OpenAI adapter: effort preferred; else max_tokens; nil reasoning → no param.
func TestOpenAIReasoningParam(t *testing.T) {
	m := &openAIModel{modelID: "x"}
	base := []*genai.Content{genai.NewContentFromText("hi", genai.RoleUser)}

	if out := m.buildRequest(&LLMRequest{Contents: base}); out.Reasoning != nil {
		t.Fatalf("no reasoning → want nil param, got %+v", out.Reasoning)
	}
	out := m.buildRequest(&LLMRequest{Contents: base, Reasoning: &ReasoningConfig{Effort: "high", BudgetTokens: 5000}})
	if out.Reasoning == nil || out.Reasoning.Effort != "high" || out.Reasoning.MaxTokens != 0 {
		t.Fatalf("effort preferred, got %+v", out.Reasoning)
	}
	out2 := m.buildRequest(&LLMRequest{Contents: base, Reasoning: &ReasoningConfig{BudgetTokens: 5000}})
	if out2.Reasoning == nil || out2.Reasoning.MaxTokens != 5000 || out2.Reasoning.Effort != "" {
		t.Fatalf("budget → max_tokens, got %+v", out2.Reasoning)
	}
}

// Anthropic adapter: thinking budget from effort; max_tokens bumped above the
// budget; temperature/top_p dropped when thinking is on.
func TestAnthropicThinkingParam(t *testing.T) {
	m := &anthropicModel{modelID: "claude-x"}
	temp := float32(0.7)
	cfg := &genai.GenerateContentConfig{Temperature: &temp, MaxOutputTokens: 1000}
	base := []*genai.Content{genai.NewContentFromText("hi", genai.RoleUser)}

	// No reasoning → no thinking, temperature kept.
	if out := m.buildRequest(&LLMRequest{Config: cfg, Contents: base}); out.Thinking != nil || out.Temperature == nil {
		t.Fatalf("no reasoning → want no thinking + temp kept, got thinking=%+v temp=%v", out.Thinking, out.Temperature)
	}
	// Effort=medium → budget 8192, max_tokens bumped above it, temp dropped.
	out := m.buildRequest(&LLMRequest{Config: cfg, Contents: base, Reasoning: &ReasoningConfig{Effort: "medium"}})
	if out.Thinking == nil || out.Thinking.Type != "enabled" || out.Thinking.BudgetTokens != 8192 {
		t.Fatalf("want thinking budget 8192, got %+v", out.Thinking)
	}
	if out.MaxTokens <= out.Thinking.BudgetTokens {
		t.Fatalf("max_tokens (%d) must exceed budget (%d)", out.MaxTokens, out.Thinking.BudgetTokens)
	}
	if out.Temperature != nil || out.TopP != nil {
		t.Fatalf("temperature/top_p must be dropped while thinking, got temp=%v topP=%v", out.Temperature, out.TopP)
	}
	// Explicit budget wins over effort mapping.
	out2 := m.buildRequest(&LLMRequest{Config: cfg, Contents: base, Reasoning: &ReasoningConfig{Effort: "low", BudgetTokens: 3000}})
	if out2.Thinking == nil || out2.Thinking.BudgetTokens != 3000 {
		t.Fatalf("explicit budget should win, got %+v", out2.Thinking)
	}
}
