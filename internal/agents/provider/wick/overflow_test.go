package wick

import (
	"context"
	"errors"
	"iter"
	"strings"
	"testing"

	"google.golang.org/genai"
)

// overflow_test.go covers the context-overflow fixes: tool-aware token
// estimation, the aggressive-compact-and-retry recovery on a vendor
// "prompt too long" error, and usage-based calibration.

// ── B1: estimator counts tool call/result payloads ──────────────────

func TestEstimateTokens_CountsToolPayloads(t *testing.T) {
	// A turn whose only weight is a big FunctionResponse (no p.Text) — the
	// exact shape the old estimator ignored, which caused the budget to be
	// compared against a fraction of the real prompt.
	bigResult := strings.Repeat("y", 40_000)
	toolTurn := &genai.Content{Role: genai.RoleUser, Parts: []*genai.Part{{
		FunctionResponse: &genai.FunctionResponse{
			Name:     "shell",
			Response: map[string]any{"result": bigResult},
		},
	}}}

	got := estimateTokens([]*genai.Content{toolTurn})
	// ~40k chars / 4 ≈ 10k tokens. The old (text-only) estimator returned 0.
	if got < 9_000 {
		t.Fatalf("tool result not counted: estimate=%d (want ~10k)", got)
	}

	// A FunctionCall's args must count too.
	callTurn := &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{{
		FunctionCall: &genai.FunctionCall{
			Name: "shell",
			Args: map[string]any{"command": strings.Repeat("z", 8_000)},
		},
	}}}
	if c := estimateTokens([]*genai.Content{callTurn}); c < 1_500 {
		t.Fatalf("tool call args not counted: estimate=%d", c)
	}
}

// ── B4: overflow → aggressive compact → retry ───────────────────────

// scriptedModel returns a queued (response, error) per call so a test can
// simulate an overflow error followed by a success. summarize() calls it
// too, so the script must account for those.
type scriptedModel struct {
	steps []step
	i     int
}

type step struct {
	resp *LLMResponse
	err  error
}

func (m *scriptedModel) Name() string { return "scripted" }

func (m *scriptedModel) GenerateContent(ctx context.Context, req *LLMRequest, stream bool) iter.Seq2[*LLMResponse, error] {
	return func(yield func(*LLMResponse, error) bool) {
		i := m.i
		m.i++
		if i >= len(m.steps) {
			yield(&LLMResponse{Content: &genai.Content{Role: genai.RoleModel}}, nil)
			return
		}
		s := m.steps[i]
		yield(s.resp, s.err)
	}
}

func TestGenerateOverflowRecovery_CompactsAndRetries(t *testing.T) {
	overflow := errors.New("HTTP 400: This model's maximum prompt length is 500000 but the request contains 557641 tokens")
	m := &scriptedModel{steps: []step{
		{err: overflow},                          // 1: first generate → overflow
		{resp: textResp("SUMMARY of old turns")}, // 2: summarize() during compact
		{resp: textResp("recovered answer")},     // 3: retry generate → success
	}}
	eng, lines := newTestEngine(m, nil)
	eng.setContextBudget(1000)
	// Enough turns that compactAggressively has something to fold.
	eng.history = []*genai.Content{
		genai.NewContentFromText("u1 "+bigText(2000), genai.RoleUser),
		genai.NewContentFromText("a1 "+bigText(2000), genai.RoleModel),
		genai.NewContentFromText("u2 "+bigText(2000), genai.RoleUser),
		genai.NewContentFromText("a2 "+bigText(2000), genai.RoleModel),
		genai.NewContentFromText("u3 "+bigText(2000), genai.RoleUser),
		genai.NewContentFromText("a3 "+bigText(2000), genai.RoleModel),
	}

	out, err := eng.generateWithOverflowRecovery(context.Background())
	if err != nil {
		t.Fatalf("recovery should succeed, got err: %v", err)
	}
	if out == nil || out.Content == nil || out.Content.Parts[0].Text != "recovered answer" {
		t.Fatalf("unexpected recovered response: %+v", out)
	}
	// A summary note must now head the history (compaction happened).
	if !strings.Contains(eng.history[0].Parts[0].Text, "SUMMARY") {
		t.Errorf("history not compacted during recovery; head=%q", eng.history[0].Parts[0].Text)
	}
	// The recovery emitted a thinking note so the user sees it's healing.
	var sawNote bool
	for _, ln := range *lines {
		if strings.Contains(ln, "auto-summarized") {
			sawNote = true
		}
	}
	if !sawNote {
		t.Error("expected a thinking note about auto-summarizing")
	}
}

func TestGenerateOverflowRecovery_NonOverflowPassesThrough(t *testing.T) {
	authErr := errors.New("HTTP 401: invalid api key")
	m := &scriptedModel{steps: []step{{err: authErr}}}
	eng, _ := newTestEngine(m, nil)
	eng.setContextBudget(1000)
	eng.history = []*genai.Content{
		genai.NewContentFromText(bigText(2000), genai.RoleUser),
		genai.NewContentFromText(bigText(2000), genai.RoleModel),
		genai.NewContentFromText(bigText(2000), genai.RoleUser),
		genai.NewContentFromText(bigText(2000), genai.RoleModel),
	}
	_, err := eng.generateWithOverflowRecovery(context.Background())
	if err == nil {
		t.Fatal("a 401 must not be swallowed by overflow recovery")
	}
	// Only one call: no compaction/retry for a non-overflow error.
	if m.i != 1 {
		t.Errorf("expected exactly 1 model call for non-overflow error, got %d", m.i)
	}
}

func TestIsContextOverflowError(t *testing.T) {
	yes := []string{
		"maximum prompt length is 500000 but the request contains 557641 tokens",
		"This model's maximum context length is 128000 tokens",
		"context_length_exceeded",
		"prompt is too long: 250000 tokens",
		"HTTP 413 request entity too large",
	}
	for _, s := range yes {
		if !isContextOverflowError(errors.New(s)) {
			t.Errorf("should classify as overflow: %q", s)
		}
	}
	no := []string{"401 unauthorized", "model not found", "429 rate limit", ""}
	for _, s := range no {
		if isContextOverflowError(errors.New(s)) {
			t.Errorf("should NOT be overflow: %q", s)
		}
	}
}

// ── B3: calibration ratchets up from real usage ─────────────────────

func TestCalibrateFromUsage_RatchetsUp(t *testing.T) {
	eng, _ := newTestEngine(&fakeModel{}, nil)
	eng.tokenCalibration = 1.0

	sent := []*genai.Content{genai.NewContentFromText(bigText(4000), genai.RoleUser)} // est ~1000
	// Vendor says the real prompt was 2000 tokens (2x the estimate).
	out := &LLMResponse{UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 2000}}

	eng.calibrateFromUsage(sent, out)
	if eng.tokenCalibration <= 1.0 {
		t.Fatalf("calibration should rise above 1.0, got %f", eng.tokenCalibration)
	}
	// calibratedEstimate must now exceed the raw estimate.
	if eng.calibratedEstimate(sent) <= estimateTokens(sent) {
		t.Error("calibrated estimate should exceed raw estimate after ratchet")
	}
}

func TestCalibrateFromUsage_NeverBelowOne(t *testing.T) {
	eng, _ := newTestEngine(&fakeModel{}, nil)
	eng.tokenCalibration = 1.0
	sent := []*genai.Content{genai.NewContentFromText(bigText(8000), genai.RoleUser)} // est ~2000
	// Vendor real is LOWER than estimate (overcount) — must not drag factor <1.
	out := &LLMResponse{UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 500}}
	eng.calibrateFromUsage(sent, out)
	if eng.tokenCalibration < 1.0 {
		t.Errorf("calibration must never drop below 1.0, got %f", eng.tokenCalibration)
	}
}
