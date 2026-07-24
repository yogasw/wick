package wick

import (
	"context"
	"fmt"
	"strings"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// resolveModel turns a resolved WickModel (plaintext key already
// decrypted by the caller) into an LLM adapter. All three kinds are
// wick-native adapters over the vendor APIs (Gemini via the genai SDK
// client; OpenAI/Anthropic via wick HTTP) — no adk dependency.
func resolveModel(ctx context.Context, m provider.WickModel) (LLM, error) {
	kind := strings.ToLower(strings.TrimSpace(m.Kind))
	format := strings.ToLower(strings.TrimSpace(m.APIFormat))
	if format == "" {
		format = defaultFormatForKind(kind)
	}
	switch format {
	case "gemini":
		return newGeminiModel(ctx, m)
	case "openai_chat", "openai_responses":
		return newOpenAIModel(m), nil
	case "anthropic_messages":
		return newAnthropicModel(m), nil
	default:
		return nil, fmt.Errorf("wick: unsupported api format %q (kind %q)", format, kind)
	}
}

// defaultFormatForKind maps a vendor family to its default wire format
// so the common cases need no explicit APIFormat.
func defaultFormatForKind(kind string) string {
	switch kind {
	case "google", "gemini", "":
		return "gemini"
	case "anthropic", "claude":
		return "anthropic_messages"
	case "openai", "openrouter", "other":
		return "openai_chat"
	default:
		return "openai_chat"
	}
}

// defaultBaseURL returns the vendor endpoint when the model didn't set
// one. "other" MUST set BaseURL (validated on save), so it has none here.
func defaultBaseURL(kind, format string) string {
	switch kind {
	case "openai":
		return "https://api.openai.com/v1"
	case "openrouter":
		return "https://openrouter.ai/api/v1"
	case "anthropic", "claude":
		return "https://api.anthropic.com/v1"
	}
	if format == "anthropic_messages" {
		return "https://api.anthropic.com/v1"
	}
	return "https://api.openai.com/v1"
}
