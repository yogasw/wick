package wick

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/genai"
)

// bigText returns a string of n 'x' runes (~n/4 tokens by the estimator).
func bigText(n int) string { return strings.Repeat("x", n) }

// TestMaybeCompact_TriggersAndSummarizes: when history exceeds the
// budget, maybeCompact asks the model to summarize the oldest turns and
// replaces them with a single model-authored summary note.
func TestMaybeCompact_TriggersAndSummarizes(t *testing.T) {
	// Model returns a canned summary for the summarize() call.
	f := &fakeModel{responses: []*LLMResponse{textResp("SUMMARY: user set up X, decided Y")}}
	eng, _ := newTestEngine(f, nil)

	// 6 turns, each ~1000 chars ≈ 250 tokens → ~1500 tokens total.
	eng.history = []*genai.Content{
		genai.NewContentFromText("u1 "+bigText(1000), genai.RoleUser),
		genai.NewContentFromText("a1 "+bigText(1000), genai.RoleModel),
		genai.NewContentFromText("u2 "+bigText(1000), genai.RoleUser),
		genai.NewContentFromText("a2 "+bigText(1000), genai.RoleModel),
		genai.NewContentFromText("u3 "+bigText(1000), genai.RoleUser),
		genai.NewContentFromText("a3 "+bigText(1000), genai.RoleModel),
	}
	before := len(eng.history)

	// Budget 1000 tokens; 0.8 trigger = 800. Estimate ~1500 > 800 → fires.
	did := eng.maybeCompact(context.Background(), 1000)
	if !did {
		t.Fatal("expected compaction to trigger")
	}
	if len(eng.history) >= before {
		t.Fatalf("history not shrunk: before=%d after=%d", before, len(eng.history))
	}
	// First content is the model-authored summary note.
	head := eng.history[0]
	if head.Role != genai.RoleModel || !strings.Contains(head.Parts[0].Text, "SUMMARY:") {
		t.Fatalf("first content should be the summary note, got %+v", head)
	}
	// The model was asked to summarize (its only call this test).
	if f.calls != 1 {
		t.Errorf("expected 1 summarize call, got %d", f.calls)
	}
}

// TestMaybeCompact_BelowBudgetNoop: under budget, nothing happens and no
// model call is made.
func TestMaybeCompact_BelowBudgetNoop(t *testing.T) {
	f := &fakeModel{responses: []*LLMResponse{textResp("should not be called")}}
	eng, _ := newTestEngine(f, nil)
	eng.history = []*genai.Content{
		genai.NewContentFromText("hi", genai.RoleUser),
		genai.NewContentFromText("yo", genai.RoleModel),
		genai.NewContentFromText("more", genai.RoleUser),
		genai.NewContentFromText("ok", genai.RoleModel),
	}
	if eng.maybeCompact(context.Background(), 100000) {
		t.Fatal("should not compact under budget")
	}
	if f.calls != 0 {
		t.Errorf("no model call expected, got %d", f.calls)
	}
}

// TestMaybeCompact_ZeroBudgetDisabled: budget 0 = unbounded, never compacts.
func TestMaybeCompact_ZeroBudgetDisabled(t *testing.T) {
	f := &fakeModel{responses: []*LLMResponse{textResp("x")}}
	eng, _ := newTestEngine(f, nil)
	eng.history = []*genai.Content{
		genai.NewContentFromText(bigText(100000), genai.RoleUser),
		genai.NewContentFromText(bigText(100000), genai.RoleModel),
		genai.NewContentFromText(bigText(100000), genai.RoleUser),
		genai.NewContentFromText(bigText(100000), genai.RoleModel),
	}
	if eng.maybeCompact(context.Background(), 0) {
		t.Fatal("budget 0 must disable compaction")
	}
}

// TestMaybeCompact_CutAlignsToUserBoundary: the kept tail must start on a
// user turn so a tool_result is never orphaned from its call.
func TestMaybeCompact_CutAlignsToUserBoundary(t *testing.T) {
	f := &fakeModel{responses: []*LLMResponse{textResp("sum")}}
	eng, _ := newTestEngine(f, nil)
	eng.history = []*genai.Content{
		genai.NewContentFromText("u1 "+bigText(1000), genai.RoleUser),
		genai.NewContentFromText("a1 "+bigText(1000), genai.RoleModel),
		genai.NewContentFromText("u2 "+bigText(1000), genai.RoleUser),
		genai.NewContentFromText("a2 "+bigText(1000), genai.RoleModel),
	}
	if !eng.maybeCompact(context.Background(), 500) {
		t.Fatal("expected compaction")
	}
	// After the summary note (index 0), the next real turn must be a user turn.
	if len(eng.history) < 2 {
		t.Fatalf("unexpected history len %d", len(eng.history))
	}
	if eng.history[1].Role != genai.RoleUser {
		t.Errorf("kept tail should start on a user turn, got role %q", eng.history[1].Role)
	}
}
