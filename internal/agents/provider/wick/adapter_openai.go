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

// openAIModel implements LLM against the OpenAI Chat Completions
// API. It covers the openai, openrouter, and OpenAI-compatible "other"
// kinds — the request/response shape is shared across all three; only
// the base URL and key differ.
type openAIModel struct {
	modelID string
	apiKey  string
	baseURL string
	// custom holds the model's parsed custom headers, applied over the
	// auth header on every call. See headers.go.
	custom map[string]string
}

func newOpenAIModel(m provider.WickModel) *openAIModel {
	base := strings.TrimRight(m.BaseURL, "/")
	if base == "" {
		base = defaultBaseURL(strings.ToLower(m.Kind), "openai_chat")
	}
	return &openAIModel{modelID: m.Model, apiKey: m.APIKey, baseURL: base, custom: ParseHeaders(m.Headers)}
}

func (m *openAIModel) Name() string { return m.modelID }

// headers builds the request headers: auth first, then the user's custom
// headers, which may override it.
func (m *openAIModel) headers() map[string]string {
	h := map[string]string{"Authorization": "Bearer " + m.apiKey}
	for k, v := range m.custom {
		h[k] = v
	}
	return h
}

func (m *openAIModel) GenerateContent(ctx context.Context, req *LLMRequest, stream bool) iter.Seq2[*LLMResponse, error] {
	if stream {
		return m.generateStream(ctx, req)
	}
	return func(yield func(*LLMResponse, error) bool) {
		body := m.buildRequest(req)
		var resp oaiResponse
		if err := postJSON(ctx, m.baseURL+"/chat/completions", m.headers(), body, &resp); err != nil {
			yield(nil, err)
			return
		}
		yield(m.toLLMResponse(&resp), nil)
	}
}

// generateStream runs the SSE (stream:true) path: it yields text deltas live
// as chunks arrive, accumulates tool-call fragments (OpenAI streams tool_calls
// piecemeal, keyed by index — name+args come in pieces), and finishes with one
// aggregated yield carrying the full Content + usage. The `[DONE]` sentinel
// terminates the stream.
func (m *openAIModel) generateStream(ctx context.Context, req *LLMRequest) iter.Seq2[*LLMResponse, error] {
	return func(yield func(*LLMResponse, error) bool) {
		body := m.buildRequest(req)
		body.Stream = true
		body.StreamOptions = &oaiStreamOptions{IncludeUsage: true}

		agg := &oaiStreamAggregator{}
		yieldErr := false
		err := postSSE(ctx, m.baseURL+"/chat/completions", m.headers(), body, func(ev sseEvent) bool {
			if strings.TrimSpace(ev.Data) == "[DONE]" {
				return false
			}
			var chunk oaiStreamChunk
			if json.Unmarshal([]byte(ev.Data), &chunk) != nil {
				return true // skip a malformed frame rather than kill the stream
			}
			if chunk.Error != nil {
				yieldErr = !yield(&LLMResponse{ErrorMessage: chunk.Error.Message, ErrorCode: chunk.Error.Code}, nil)
				return false
			}
			if delta := agg.ingest(&chunk); delta != "" {
				yieldErr = !yield(&LLMResponse{TextDelta: delta}, nil)
				if yieldErr {
					return false
				}
			}
			return true
		})
		if yieldErr {
			return // consumer stopped; nothing more to yield
		}
		if err != nil {
			yield(nil, err)
			return
		}
		yield(agg.final(), nil)
	}
}

// ── request construction ──────────────────────────────────────────────

type oaiMessage struct {
	Role string `json:"role"`
	// Content is a plain string for text-only messages, or an array of
	// oaiContentPart (text + image_url) for a multimodal user message. `any`
	// so both shapes marshal correctly — OpenAI/OpenRouter accept either.
	Content    any           `json:"content,omitempty"`
	ToolCalls  []oaiToolCall `json:"tool_calls,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	Name       string        `json:"name,omitempty"`
}

// oaiContentPart is one element of a multimodal message content array:
// {type:"text",text:...} or {type:"image_url",image_url:{url:"data:...;base64,..."}}.
type oaiContentPart struct {
	Type     string       `json:"type"`
	Text     string       `json:"text,omitempty"`
	ImageURL *oaiImageURL `json:"image_url,omitempty"`
}

type oaiImageURL struct {
	URL string `json:"url"`
}

type oaiToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function oaiToolCallFunc `json:"function"`
}

type oaiToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type oaiRequest struct {
	Model         string            `json:"model"`
	Messages      []oaiMessage      `json:"messages"`
	Tools         []oaiTool         `json:"tools,omitempty"`
	MaxTokens     int               `json:"max_tokens,omitempty"`
	Temperature   *float32          `json:"temperature,omitempty"`
	TopP          *float32          `json:"top_p,omitempty"`
	Reasoning     *oaiReasoning     `json:"reasoning,omitempty"`
	Stream        bool              `json:"stream,omitempty"`
	StreamOptions *oaiStreamOptions `json:"stream_options,omitempty"`
}

// oaiStreamOptions asks the server to include a final usage chunk when
// streaming (OpenAI/OpenRouter omit usage from streamed responses by
// default), so the engine still gets token counts for calibration.
type oaiStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// oaiReasoning is the OpenRouter/OpenAI-compatible reasoning param. Effort is
// the enum form ("low"|"medium"|"high"); MaxTokens the Anthropic-style token
// form. OpenRouter normalizes either across vendors; a native OpenAI endpoint
// reads effort. Only one is sent (effort preferred).
type oaiReasoning struct {
	Effort    string `json:"effort,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
}

type oaiTool struct {
	Type     string         `json:"type"`
	Function oaiToolFuncDef `json:"function"`
}

type oaiToolFuncDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

func (m *openAIModel) buildRequest(req *LLMRequest) *oaiRequest {
	out := &oaiRequest{Model: m.modelID}
	if sys := systemText(req.Config); sys != "" {
		out.Messages = append(out.Messages, oaiMessage{Role: "system", Content: sys})
	}
	for _, c := range req.Contents {
		out.Messages = append(out.Messages, contentToOAI(c)...)
	}
	for _, fd := range toolsFromConfig(req.Config) {
		out.Tools = append(out.Tools, oaiTool{
			Type: "function",
			Function: oaiToolFuncDef{
				Name:        fd.Name,
				Description: fd.Description,
				Parameters:  schemaToJSON(fd.Parameters),
			},
		})
	}
	if req.Config != nil {
		out.Temperature = req.Config.Temperature
		out.TopP = req.Config.TopP
		if req.Config.MaxOutputTokens > 0 {
			out.MaxTokens = int(req.Config.MaxOutputTokens)
		}
	}
	// Reasoning: prefer the effort enum; else the token budget. OpenRouter
	// translates either to the underlying model's native param, so this one
	// shape covers openai / openrouter / grok / other.
	if r := req.Reasoning; r != nil {
		if r.Effort != "" {
			out.Reasoning = &oaiReasoning{Effort: r.Effort}
		} else if r.BudgetTokens > 0 {
			out.Reasoning = &oaiReasoning{MaxTokens: r.BudgetTokens}
		}
	}
	return out
}

// contentToOAI maps one genai.Content to one or more OpenAI messages.
// A model content with function calls becomes an assistant message with
// tool_calls; a user content carrying function responses becomes one
// tool message per response; plain text maps by role.
func contentToOAI(c *genai.Content) []oaiMessage {
	if c == nil {
		return nil
	}
	var text strings.Builder
	var toolCalls []oaiToolCall
	var toolMsgs []oaiMessage
	var images []oaiContentPart
	for _, p := range c.Parts {
		switch {
		case p == nil:
		case p.FunctionCall != nil:
			args, _ := json.Marshal(p.FunctionCall.Args)
			toolCalls = append(toolCalls, oaiToolCall{
				ID:   p.FunctionCall.ID,
				Type: "function",
				Function: oaiToolCallFunc{
					Name:      p.FunctionCall.Name,
					Arguments: string(args),
				},
			})
		case p.FunctionResponse != nil:
			toolMsgs = append(toolMsgs, oaiMessage{
				Role:       "tool",
				ToolCallID: p.FunctionResponse.ID,
				Name:       p.FunctionResponse.Name,
				Content:    responseText(p.FunctionResponse.Response),
			})
		case p.InlineData != nil && len(p.InlineData.Data) > 0:
			// image_url with a base64 data-URL — OpenAI's multimodal shape,
			// which OpenRouter forwards to any vision model (incl. Claude/Grok).
			url := "data:" + p.InlineData.MIMEType + ";base64," + base64.StdEncoding.EncodeToString(p.InlineData.Data)
			images = append(images, oaiContentPart{Type: "image_url", ImageURL: &oaiImageURL{URL: url}})
		case p.Text != "" && !p.Thought:
			text.WriteString(p.Text)
		}
	}
	if len(toolMsgs) > 0 {
		return toolMsgs
	}
	role := "user"
	if c.Role == genai.RoleModel {
		role = "assistant"
	}
	msg := oaiMessage{Role: role}
	if len(images) > 0 {
		// Multimodal: content is an array — a text part (if any) then the
		// image parts. Scalar-string form is kept for the text-only path so
		// existing behaviour is byte-identical when there are no images.
		parts := make([]oaiContentPart, 0, len(images)+1)
		if t := text.String(); t != "" {
			parts = append(parts, oaiContentPart{Type: "text", Text: t})
		}
		parts = append(parts, images...)
		msg.Content = parts
	} else {
		msg.Content = text.String()
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}
	if len(images) == 0 && text.Len() == 0 && len(msg.ToolCalls) == 0 {
		return nil
	}
	return []oaiMessage{msg}
}

// ── response parsing ───────────────────────────────────────────────────

type oaiResponse struct {
	Choices []struct {
		Message struct {
			Content   string        `json:"content"`
			ToolCalls []oaiToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		TotalTokens         int `json:"total_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

// ── streaming ──────────────────────────────────────────────────────────

// oaiUsage is the token accounting block, shared by the non-streaming
// response and the streamed trailing usage chunk.
type oaiUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

// oaiStreamChunk is one `chat.completions.chunk` SSE frame: a delta on the
// first (only) choice, plus an optional trailing usage block (when
// stream_options.include_usage was set).
type oaiStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *oaiUsage `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"error"`
}

// oaiStreamAggregator folds streamed chunks into a final response: text is
// concatenated, tool calls are reassembled by index (id/name arrive once, the
// arguments string streams in fragments), and the trailing usage chunk is kept.
type oaiStreamAggregator struct {
	text  strings.Builder
	calls []oaiToolCall // dense by first-seen order; index maps into this
	byIdx map[int]int   // chunk tool-call index → position in calls
	usage *oaiUsage
}

// ingest folds one chunk in and returns any text delta to emit live (empty
// when the chunk carried only tool-call fragments or usage).
func (a *oaiStreamAggregator) ingest(c *oaiStreamChunk) string {
	if c.Usage != nil {
		a.usage = c.Usage
	}
	if len(c.Choices) == 0 {
		return ""
	}
	d := c.Choices[0].Delta
	for _, tc := range d.ToolCalls {
		if a.byIdx == nil {
			a.byIdx = map[int]int{}
		}
		pos, ok := a.byIdx[tc.Index]
		if !ok {
			pos = len(a.calls)
			a.byIdx[tc.Index] = pos
			a.calls = append(a.calls, oaiToolCall{Type: "function"})
		}
		if tc.ID != "" {
			a.calls[pos].ID = tc.ID
		}
		if tc.Type != "" {
			a.calls[pos].Type = tc.Type
		}
		if tc.Function.Name != "" {
			a.calls[pos].Function.Name = tc.Function.Name
		}
		a.calls[pos].Function.Arguments += tc.Function.Arguments
	}
	if d.Content != "" {
		a.text.WriteString(d.Content)
	}
	return d.Content
}

// final builds the aggregated LLMResponse from everything ingested — the same
// shape toLLMResponse produces for the non-streaming path, so the engine and
// history handle a streamed turn identically.
func (a *oaiStreamAggregator) final() *LLMResponse {
	out := &LLMResponse{}
	parts := make([]*genai.Part, 0, 1+len(a.calls))
	if t := a.text.String(); t != "" {
		parts = append(parts, &genai.Part{Text: t})
	}
	for _, tc := range a.calls {
		var args map[string]any
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
		parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: args,
		}})
	}
	out.Content = &genai.Content{Role: genai.RoleModel, Parts: parts}
	if u := a.usage; u != nil {
		out.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:        int32(u.PromptTokens),
			CandidatesTokenCount:    int32(u.CompletionTokens),
			CachedContentTokenCount: int32(u.PromptTokensDetails.CachedTokens),
			TotalTokenCount:         int32(u.TotalTokens),
		}
	}
	return out
}

func (m *openAIModel) toLLMResponse(r *oaiResponse) *LLMResponse {
	out := &LLMResponse{}
	if r.Error != nil {
		out.ErrorMessage = r.Error.Message
		out.ErrorCode = r.Error.Code
		return out
	}
	if len(r.Choices) == 0 {
		out.ErrorMessage = "no choices in response"
		return out
	}
	ch := r.Choices[0]
	parts := make([]*genai.Part, 0, 1+len(ch.Message.ToolCalls))
	if ch.Message.Content != "" {
		parts = append(parts, &genai.Part{Text: ch.Message.Content})
	}
	for _, tc := range ch.Message.ToolCalls {
		var args map[string]any
		_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
		parts = append(parts, &genai.Part{FunctionCall: &genai.FunctionCall{
			ID:   tc.ID,
			Name: tc.Function.Name,
			Args: args,
		}})
	}
	out.Content = &genai.Content{Role: genai.RoleModel, Parts: parts}
	// OpenAI-compatible prompt caching is automatic server-side; the
	// cached-token count comes back in prompt_tokens_details. Surface it
	// as CachedContentTokenCount for cache-hit visibility.
	out.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
		PromptTokenCount:        int32(r.Usage.PromptTokens),
		CandidatesTokenCount:    int32(r.Usage.CompletionTokens),
		CachedContentTokenCount: int32(r.Usage.PromptTokensDetails.CachedTokens),
		TotalTokenCount:         int32(r.Usage.TotalTokens),
	}
	return out
}

// responseText renders a function-response payload back to a string for
// the vendor. The engine stores results under {"result": "..."}.
func responseText(resp map[string]any) string {
	if resp == nil {
		return ""
	}
	if s, ok := resp["result"].(string); ok {
		return s
	}
	b, _ := json.Marshal(resp)
	return string(b)
}
