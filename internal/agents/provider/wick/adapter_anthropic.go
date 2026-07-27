package wick

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"iter"
	"strings"

	"google.golang.org/genai"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// anthropicVersion is the required API version header value.
const anthropicVersion = "2023-06-01"

// anthropicDefaultMaxTokens is used when the model config sets none —
// Anthropic's Messages API requires max_tokens.
const anthropicDefaultMaxTokens = 4096

// anthropicModel implements LLM against the Anthropic Messages API.
type anthropicModel struct {
	modelID string
	apiKey  string
	baseURL string
}

func newAnthropicModel(m provider.WickModel) *anthropicModel {
	base := strings.TrimRight(m.BaseURL, "/")
	if base == "" {
		base = defaultBaseURL(strings.ToLower(m.Kind), "anthropic_messages")
	}
	return &anthropicModel{modelID: m.Model, apiKey: m.APIKey, baseURL: base}
}

func (m *anthropicModel) Name() string { return m.modelID }

func (m *anthropicModel) GenerateContent(ctx context.Context, req *LLMRequest, stream bool) iter.Seq2[*LLMResponse, error] {
	return func(yield func(*LLMResponse, error) bool) {
		body := m.buildRequest(req)
		headers := map[string]string{
			"x-api-key":         m.apiKey,
			"anthropic-version": anthropicVersion,
		}
		var resp antResponse
		if err := postJSON(ctx, m.baseURL+"/messages", headers, body, &resp); err != nil {
			yield(nil, err)
			return
		}
		yield(m.toLLMResponse(&resp), nil)
	}
}

// ── request ────────────────────────────────────────────────────────────

type antRequest struct {
	Model string `json:"model"`
	// System is the structured form (array of text blocks) so the static
	// system prefix can carry a cache_control breakpoint (prompt caching).
	System      []antTextBlock `json:"system,omitempty"`
	MaxTokens   int            `json:"max_tokens"`
	Messages    []antMessage   `json:"messages"`
	Tools       []antTool      `json:"tools,omitempty"`
	Temperature *float32       `json:"temperature,omitempty"`
	TopP        *float32       `json:"top_p,omitempty"`
	Thinking    *antThinking   `json:"thinking,omitempty"`
}

// antThinking is Anthropic's extended-thinking param: {type:"enabled",
// budget_tokens:N}. Anthropic has no effort enum, so an effort level maps to a
// token budget (antThinkingBudgetForEffort).
type antThinking struct {
	Type         string `json:"type"` // "enabled"
	BudgetTokens int    `json:"budget_tokens"`
}

// antThinkingBudgetForEffort maps the vendor-agnostic effort level to a
// concrete Anthropic thinking budget (tokens). Rough tiers — the point is a
// sensible non-zero budget per level; operators wanting an exact number set
// ThinkingBudget directly.
func antThinkingBudgetForEffort(effort string) int {
	switch effort {
	case "low":
		return 2048
	case "high":
		return 16384
	case "medium":
		return 8192
	default:
		return 0
	}
}

// antCacheControl marks a block as a caching breakpoint. Anthropic
// caches everything up to and including the marked block, so we place it
// on the last tool + the system block — the byte-stable static prefix.
type antCacheControl struct {
	Type string `json:"type"` // "ephemeral"
}

type antTextBlock struct {
	Type         string           `json:"type"` // "text"
	Text         string           `json:"text"`
	CacheControl *antCacheControl `json:"cache_control,omitempty"`
}

type antMessage struct {
	Role    string     `json:"role"`
	Content []antBlock `json:"content"`
}

type antBlock struct {
	Type string `json:"type"`
	// text
	Text string `json:"text,omitempty"`
	// tool_use
	ID    string         `json:"id,omitempty"`
	Name  string         `json:"name,omitempty"`
	Input map[string]any `json:"input,omitempty"`
	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
	// image
	Source *antImageSource `json:"source,omitempty"`
}

// antImageSource is the base64 image payload for an "image" block:
// {type:"base64", media_type:"image/png", data:"<b64>"}.
type antImageSource struct {
	Type      string `json:"type"`       // "base64"
	MediaType string `json:"media_type"` // e.g. "image/png"
	Data      string `json:"data"`       // base64-encoded bytes
}

type antTool struct {
	Name         string           `json:"name"`
	Description  string           `json:"description,omitempty"`
	InputSchema  map[string]any   `json:"input_schema"`
	CacheControl *antCacheControl `json:"cache_control,omitempty"`
}

func (m *anthropicModel) buildRequest(req *LLMRequest) *antRequest {
	out := &antRequest{Model: m.modelID, MaxTokens: anthropicDefaultMaxTokens}
	// Prompt caching: the system prompt is the biggest byte-stable prefix,
	// so mark it as an ephemeral cache breakpoint. Anthropic caches it +
	// everything before it (the tools, marked below) across turns, so only
	// the growing message tail is re-processed each turn.
	if sys := systemText(req.Config); sys != "" {
		out.System = []antTextBlock{{
			Type:         "text",
			Text:         sys,
			CacheControl: &antCacheControl{Type: "ephemeral"},
		}}
	}
	if req.Config != nil {
		out.Temperature = req.Config.Temperature
		out.TopP = req.Config.TopP
		if req.Config.MaxOutputTokens > 0 {
			out.MaxTokens = int(req.Config.MaxOutputTokens)
		}
	}
	// Extended thinking: budget tokens directly, or derived from the effort
	// level. Anthropic requires budget_tokens < max_tokens and rejects an
	// explicit temperature/top_p while thinking is on, so bump max_tokens above
	// the budget when needed and drop the sampling knobs.
	if r := req.Reasoning; r != nil {
		budget := r.BudgetTokens
		if budget <= 0 {
			budget = antThinkingBudgetForEffort(r.Effort)
		}
		if budget > 0 {
			out.Thinking = &antThinking{Type: "enabled", BudgetTokens: budget}
			if out.MaxTokens <= budget {
				out.MaxTokens = budget + anthropicDefaultMaxTokens
			}
			out.Temperature = nil
			out.TopP = nil
		}
	}
	for _, c := range req.Contents {
		if msg, ok := contentToAnthropic(c); ok {
			out.Messages = append(out.Messages, msg)
		}
	}
	for _, fd := range toolsFromConfig(req.Config) {
		out.Tools = append(out.Tools, antTool{
			Name:        fd.Name,
			Description: fd.Description,
			InputSchema: schemaToJSON(fd.Parameters),
		})
	}
	// Mark the last tool as a cache breakpoint so the whole tool block is
	// cached alongside the system prefix (both are stable across turns).
	if n := len(out.Tools); n > 0 {
		out.Tools[n-1].CacheControl = &antCacheControl{Type: "ephemeral"}
	}
	return out
}

func contentToAnthropic(c *genai.Content) (antMessage, bool) {
	if c == nil || len(c.Parts) == 0 {
		return antMessage{}, false
	}
	role := "user"
	if c.Role == genai.RoleModel {
		role = "assistant"
	}
	var blocks []antBlock
	for _, p := range c.Parts {
		switch {
		case p == nil:
		case p.FunctionCall != nil:
			blocks = append(blocks, antBlock{
				Type:  "tool_use",
				ID:    p.FunctionCall.ID,
				Name:  p.FunctionCall.Name,
				Input: p.FunctionCall.Args,
			})
		case p.FunctionResponse != nil:
			blocks = append(blocks, antBlock{
				Type:      "tool_result",
				ToolUseID: p.FunctionResponse.ID,
				Content:   responseText(p.FunctionResponse.Response),
			})
		case p.InlineData != nil && len(p.InlineData.Data) > 0:
			blocks = append(blocks, antBlock{
				Type: "image",
				Source: &antImageSource{
					Type:      "base64",
					MediaType: p.InlineData.MIMEType,
					Data:      base64.StdEncoding.EncodeToString(p.InlineData.Data),
				},
			})
		case p.Text != "" && !p.Thought:
			blocks = append(blocks, antBlock{Type: "text", Text: p.Text})
		}
	}
	if len(blocks) == 0 {
		return antMessage{}, false
	}
	return antMessage{Role: role, Content: blocks}, true
}

// ── response ─────────────────────────────────────────────────────────────

type antResponse struct {
	Content []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens         int `json:"input_tokens"`
		OutputTokens        int `json:"output_tokens"`
		CacheReadTokens     int `json:"cache_read_input_tokens"`
		CacheCreationTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

func (m *anthropicModel) toLLMResponse(r *antResponse) *LLMResponse {
	out := &LLMResponse{}
	if r.Error != nil {
		out.ErrorMessage = r.Error.Message
		out.ErrorCode = r.Error.Type
		return out
	}
	parts := make([]*genai.Part, 0, len(r.Content))
	for _, b := range r.Content {
		switch b.Type {
		case "text":
			if b.Text != "" {
				parts = append(parts, &genai.Part{Text: b.Text})
			}
		case "tool_use":
			var args map[string]any
			_ = json.Unmarshal(b.Input, &args)
			parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{
				ID:   b.ID,
				Name: b.Name,
				Args: args,
			}})
		}
	}
	out.Content = &genai.Content{Role: genai.RoleModel, Parts: parts}
	// Anthropic reports cache-read tokens separately from fresh input
	// tokens; surface them as CachedContentTokenCount so the spawn-log /
	// UI can show prompt-cache hits.
	out.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:        int32(r.Usage.InputTokens + r.Usage.CacheReadTokens + r.Usage.CacheCreationTokens),
		CandidatesTokenCount:    int32(r.Usage.OutputTokens),
		CachedContentTokenCount: int32(r.Usage.CacheReadTokens),
		TotalTokenCount:         int32(r.Usage.InputTokens + r.Usage.CacheReadTokens + r.Usage.CacheCreationTokens + r.Usage.OutputTokens),
	}
	return out
}
