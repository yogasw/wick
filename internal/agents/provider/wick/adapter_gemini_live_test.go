package wick

import (
	"context"
	"os"
	"testing"

	provider "github.com/yogasw/wick/internal/agents/provider"
	"google.golang.org/genai"
)

// TestGeminiLiveThinkingOff hits the real Gemini API to prove the
// thinking-off encoding is accepted by BOTH model generations. It is the
// regression guard for the bare "400 INVALID_ARGUMENT / Request contains an
// invalid argument" that thinkingBudget=0 triggers on the 3.x line.
//
// Skipped unless GEMINI_API_KEY is set, so CI and offline runs stay green:
//
//	GEMINI_API_KEY=... go test ./internal/agents/provider/wick/ -run GeminiLive -v
func TestGeminiLiveThinkingOff(t *testing.T) {
	key := os.Getenv("GEMINI_API_KEY")
	if key == "" {
		t.Skip("GEMINI_API_KEY not set")
	}
	ctx := context.Background()

	// gemini-2.x is intentionally omitted: the free tier grants it zero quota,
	// so it 429s regardless of request shape and would make this test flaky.
	// The 2.x zero-budget encoding is covered by the unit test above.
	for _, model := range []string{"gemini-3.5-flash-lite", "gemini-3.6-flash"} {
		t.Run(model, func(t *testing.T) {
			llm, err := resolveModel(ctx, provider.WickModel{
				Kind:   "google",
				Model:  model,
				APIKey: key,
			})
			if err != nil {
				t.Fatalf("resolveModel: %v", err)
			}

			cfg := &genai.GenerateContentConfig{MaxOutputTokens: 16}
			applyGeminiThinking(cfg, model, nil) // nil = thinking off

			req := &LLMRequest{
				Model:    model,
				Contents: []*genai.Content{genai.NewContentFromText("say hi", genai.RoleUser)},
				Config:   cfg,
			}
			for resp, err := range llm.GenerateContent(ctx, req, false) {
				if err != nil {
					t.Fatalf("generate with thinking off: %v", err)
				}
				if resp != nil && resp.ErrorMessage != "" {
					t.Fatalf("vendor error: %s %s", resp.ErrorCode, resp.ErrorMessage)
				}
				break
			}
		})
	}
}
