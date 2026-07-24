package wick

import (
	"context"
	"errors"
	"testing"

	"google.golang.org/genai"
)

// TestGeminiAdapter_MapsResponse: the Gemini passthrough maps the first
// candidate's content + usage into wick's LLMResponse (genai types flow
// through natively — text, thinking, and function calls all preserved).
func TestGeminiAdapter_MapsResponse(t *testing.T) {
	m := &geminiModel{
		modelID: "gemini-x",
		generate: func(ctx context.Context, model string, contents []*genai.Content, cfg *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
			if model != "gemini-x" {
				t.Errorf("model passed wrong: %q", model)
			}
			return &genai.GenerateContentResponse{
				Candidates: []*genai.Candidate{{
					Content: &genai.Content{Role: genai.RoleModel, Parts: []*genai.Part{
						{Text: "hello"},
						{FunctionCall: &genai.FunctionCall{Name: "shell", Args: map[string]any{"command": "ls"}}},
					}},
				}},
				UsageMetadata: &genai.GenerateContentResponseUsageMetadata{PromptTokenCount: 11, CandidatesTokenCount: 4},
			}, nil
		},
	}
	var got *LLMResponse
	for resp, err := range m.GenerateContent(context.Background(), &LLMRequest{Model: "gemini-x"}, false) {
		if err != nil {
			t.Fatal(err)
		}
		got = resp
	}
	if got == nil || got.Content == nil || len(got.Content.Parts) != 2 {
		t.Fatalf("want 2 parts, got %+v", got)
	}
	if got.Content.Parts[0].Text != "hello" {
		t.Errorf("text part wrong: %+v", got.Content.Parts[0])
	}
	if fc := got.Content.Parts[1].FunctionCall; fc == nil || fc.Name != "shell" {
		t.Errorf("function call part wrong: %+v", got.Content.Parts[1])
	}
	if got.UsageMetadata == nil || got.UsageMetadata.PromptTokenCount != 11 {
		t.Errorf("usage not mapped: %+v", got.UsageMetadata)
	}
}

// TestGeminiAdapter_Error: a call error is yielded (surfaces as a turn
// Error in the engine).
func TestGeminiAdapter_Error(t *testing.T) {
	m := &geminiModel{
		modelID: "gemini-x",
		generate: func(ctx context.Context, model string, contents []*genai.Content, cfg *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error) {
			return nil, errors.New("401 invalid api key")
		},
	}
	var gotErr error
	for _, err := range m.GenerateContent(context.Background(), &LLMRequest{Model: "gemini-x"}, false) {
		if err != nil {
			gotErr = err
		}
	}
	if gotErr == nil {
		t.Fatal("expected error to be yielded")
	}
}
