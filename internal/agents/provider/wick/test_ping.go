package wick

import (
	"context"
	"errors"
	"time"

	provider "github.com/yogasw/wick/internal/agents/provider"
	"google.golang.org/genai"
)

// TestPingTimeout bounds a "Test" button ping — long enough for a slow
// vendor cold-start, short enough not to hang the UI on a bad endpoint.
const TestPingTimeout = 20 * time.Second

// TestModel sends a minimal 1-token generate call through the model's own
// adapter (the same resolveModel path a real spawn uses) to validate the
// key + model id + base URL in one round-trip. m.APIKey must already be
// the DECRYPTED plaintext — callers decrypt server-side before calling
// this, exactly like spawn.go does.
func TestModel(ctx context.Context, m provider.WickModel) (latency time.Duration, err error) {
	ctx, cancel := context.WithTimeout(ctx, TestPingTimeout)
	defer cancel()

	llm, err := resolveModel(ctx, m)
	if err != nil {
		return 0, err
	}

	req := &LLMRequest{
		Model:    m.Model,
		Contents: []*genai.Content{genai.NewContentFromText("hi", genai.RoleUser)},
		Config:   &genai.GenerateContentConfig{MaxOutputTokens: 1},
	}

	start := time.Now()
	for resp, genErr := range llm.GenerateContent(ctx, req, false) {
		if genErr != nil {
			return time.Since(start), genErr
		}
		if resp != nil && resp.ErrorMessage != "" {
			return time.Since(start), errors.New(resp.ErrorMessage)
		}
		// First response (success or not) ends the ping — a 1-token cap
		// means there is at most one meaningful iteration anyway.
		return time.Since(start), nil
	}
	return time.Since(start), errors.New("model returned no response")
}

// TestModelErrorMessage renders a TestModel error the same friendly way
// spawn failures are shown, so the Test button and a real spawn failure
// give the operator consistent, actionable text.
func TestModelErrorMessage(err error) string {
	return vendorErrorMessage(err)
}
