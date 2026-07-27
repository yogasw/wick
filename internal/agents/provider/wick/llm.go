package wick

import (
	"context"
	"iter"

	"google.golang.org/genai"
)

// llm.go defines wick's own model abstraction. Originally the engine
// used adk-go's model.LLM; we dropped that dependency (see plan
// "DECIDED — DROP adk") because adk's real value is its orchestration
// (llmagent / runner), which wick does not use — the harness is wick's
// own (engine.go). What remained of adk was just this interface + a
// Gemini adapter, both of which sit fine on the official genai SDK
// directly. We keep google.golang.org/genai for the Gemini client and
// the shared Content/Part type model every adapter speaks.

// LLM is the minimal contract every model adapter implements: a name
// and a (possibly streaming) content generation call. Mirrors the shape
// adk-go exposed so the adapters were a drop-in swap.
type LLM interface {
	Name() string
	GenerateContent(ctx context.Context, req *LLMRequest, stream bool) iter.Seq2[*LLMResponse, error]
}

// LLMRequest is one generation request. Contents is the full message
// history (user/model alternation); Config carries the system
// instruction, tool declarations, and generation knobs.
type LLMRequest struct {
	Model    string
	Contents []*genai.Content
	Config   *genai.GenerateContentConfig
	// Reasoning is wick's vendor-agnostic reasoning request. Each adapter
	// translates it to its own wire param (OpenAI reasoning:{effort|max_tokens},
	// Anthropic thinking:{type,budget_tokens}); Gemini reads ThinkingConfig off
	// Config instead. nil = no explicit reasoning (vendor default).
	Reasoning *ReasoningConfig
}

// ReasoningConfig is the normalized reasoning request. Effort is the preferred
// vendor-agnostic level ("low"|"medium"|"high"); BudgetTokens is the token
// alternative (Anthropic-style). An adapter uses whichever its vendor accepts,
// preferring Effort. Both zero = unset (should be nil then).
type ReasoningConfig struct {
	Effort       string
	BudgetTokens int
}

// LLMResponse is one model response. Content holds the candidate's parts
// (text / thinking / function calls); UsageMetadata carries token counts
// (incl. cached tokens) for the context-budget calibration + spawn-log.
// ErrorCode/ErrorMessage are set when the vendor returned an error the
// adapter chose to surface in-band rather than as a Go error.
type LLMResponse struct {
	Content       *genai.Content
	UsageMetadata *genai.GenerateContentResponseUsageMetadata
	ErrorCode     string
	ErrorMessage  string
}
