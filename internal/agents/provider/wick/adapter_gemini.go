package wick

import (
	"context"
	"fmt"
	"iter"
	"time"

	"github.com/rs/zerolog/log"
	"google.golang.org/genai"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// geminiModel implements LLM against Google's Gemini API via the
// official genai SDK client. This is the wick-native replacement for
// adk-go's model/gemini.NewModel (which wrapped the same client) — same
// behaviour, one fewer dependency.
type geminiModel struct {
	modelID string
	// generate is the underlying call; a field so tests can inject a fake
	// without a live genai.Client (which is a concrete, unfakeable type).
	generate func(ctx context.Context, model string, contents []*genai.Content, cfg *genai.GenerateContentConfig) (*genai.GenerateContentResponse, error)
}

func newGeminiModel(ctx context.Context, m provider.WickModel) (*geminiModel, error) {
	cfg := &genai.ClientConfig{APIKey: m.APIKey}
	if m.BaseURL != "" {
		cfg.HTTPOptions = genai.HTTPOptions{BaseURL: m.BaseURL}
	}
	client, err := genai.NewClient(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("gemini client: %w", err)
	}
	return &geminiModel{
		modelID:  m.Model,
		generate: client.Models.GenerateContent,
	}, nil
}

func (m *geminiModel) Name() string { return m.modelID }

// GenerateContent calls the Gemini API (non-streaming) and adapts the
// response to wick's LLMResponse. The engine drives non-streaming v1;
// the stream arg is accepted for interface parity and ignored.
func (m *geminiModel) GenerateContent(ctx context.Context, req *LLMRequest, stream bool) iter.Seq2[*LLMResponse, error] {
	return func(yield func(*LLMResponse, error) bool) {
		// Apply the SAME retry+timeout policy the HTTP adapters use — previously
		// Gemini had neither, so a timeout/429/5xx failed immediately (and a hung
		// call had no per-attempt ceiling). doWithRetry bounds each attempt and
		// retries transient failures with backoff; notify surfaces "retrying
		// (attempt N)" live.
		policy := retryPolicyFromContext(ctx)
		notify := retryNotifyFromContext(ctx)
		var resp *genai.GenerateContentResponse
		start := time.Now()
		err := doWithRetry(ctx, policy, notify, func(callCtx context.Context) error {
			r, e := m.generate(callCtx, m.modelID, req.Contents, req.Config)
			if e != nil {
				return e
			}
			resp = r
			return nil
		})
		if err != nil {
			log.Warn().Str("component", "wick.gemini").Str("model", m.modelID).
				Dur("latency", time.Since(start)).Err(err).Msg("outbound call: error (after retries)")
			yield(nil, err)
			return
		}
		log.Debug().Str("component", "wick.gemini").Str("model", m.modelID).
			Dur("latency", time.Since(start)).Int("candidates", len(resp.Candidates)).
			Msg("outbound call: ok")
		out := &LLMResponse{UsageMetadata: resp.UsageMetadata}
		if len(resp.Candidates) > 0 {
			out.Content = resp.Candidates[0].Content
		}
		yield(out, nil)
	}
}
