package custom

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	wfprovider "github.com/yogasw/wick/internal/agents/workflow/provider"
)

// AIParser turns an arbitrary paste (fetch() snippet, axios call, raw
// API docs, prose) into the same ParsedRequest shape the deterministic
// cURL parser produces, so both feed one review form. Implementations
// must be one-shot — no retention of the raw paste, no streaming, no
// chains.
type AIParser interface {
	Parse(ctx context.Context, paste string) (*ParsedRequest, error)
}

// AIProviderEntry is one selectable provider on the paste page's AI
// tab — the instance name the admin recognizes plus its parser.
type AIProviderEntry struct {
	Name   string
	Parser AIParser
}

//go:embed ai_parser.tmpl
var aiPromptTemplate string

// parsedRequestSchema is the strict JSON Schema the LLM must satisfy.
// Validation happens at the provider layer (structured output) and is
// re-checked here before the result reaches the review form.
var parsedRequestSchema = map[string]any{
	"type":     "object",
	"required": []string{"method", "url"},
	"properties": map[string]any{
		"method": map[string]any{
			"type": "string",
			"enum": []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"},
		},
		"url": map[string]any{"type": "string"},
		"headers": map[string]any{
			"type": "array",
			"items": map[string]any{
				"type":     "object",
				"required": []string{"key", "value"},
				"properties": map[string]any{
					"key":    map[string]any{"type": "string"},
					"value":  map[string]any{"type": "string"},
					"secret": map[string]any{"type": "boolean"},
				},
			},
		},
		"body":              map[string]any{"type": "string"},
		"content_type":      map[string]any{"type": "string"},
		"suggested_op_name": map[string]any{"type": "string"},
	},
}

// providerAIParser adapts a workflow LLM provider (Claude CLI et al.)
// into the AIParser contract. Only providers with native structured
// output qualify — see NewProviderAIParser.
type providerAIParser struct {
	prov wfprovider.Provider
}

// NewProviderAIParser wraps a structured-output-capable provider.
// Returns nil when the provider can't guarantee JSON, which keeps the
// AI tab hidden rather than flaky.
func NewProviderAIParser(p wfprovider.Provider) AIParser {
	if p == nil || !p.Capabilities().StructuredOutput {
		return nil
	}
	return &providerAIParser{prov: p}
}

func (p *providerAIParser) Parse(ctx context.Context, paste string) (*ParsedRequest, error) {
	prompt := strings.ReplaceAll(aiPromptTemplate, "{{PASTE}}", paste)
	res, err := p.prov.StructuredCall(ctx, wfprovider.StructuredRequest{
		Prompt: prompt,
		Schema: parsedRequestSchema,
	})
	if err != nil {
		return nil, fmt.Errorf("AI parser: %w", err)
	}
	if !res.OK || res.Parsed == nil {
		msg := res.Error
		if msg == "" {
			msg = "no structured result"
		}
		return nil, fmt.Errorf("AI couldn't extract a clean HTTP call from your paste — try the cURL parser or paste more context (%s)", msg)
	}
	raw, err := json.Marshal(res.Parsed)
	if err != nil {
		return nil, fmt.Errorf("AI parser: %w", err)
	}
	var parsed ParsedRequest
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("AI parser returned an unexpected shape: %w", err)
	}
	if parsed.Method == "" || parsed.URL == "" {
		return nil, fmt.Errorf("AI couldn't extract a clean HTTP call from your paste — method or URL missing")
	}
	parsed.Method = strings.ToUpper(parsed.Method)
	markSecretHeaders(parsed.Headers)
	if parsed.ContentType == "" && parsed.Body != "" && looksLikeJSON(parsed.Body) {
		parsed.ContentType = "application/json"
	}
	return &parsed, nil
}
