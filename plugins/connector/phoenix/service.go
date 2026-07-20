package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// service.go is the pure-Go layer: it validates inputs, escapes filter
// expressions, resolves time ranges, and turns Phoenix's nested
// `attributes` blob into the stable typed shapes the LLM sees. No network,
// no fixtures — every function here is unit-testable in isolation.

const previewLen = 160

// ── Typed output shapes ──────────────────────────────────────────────────

// ToolCall is one function call the model requested (or replayed back to it
// as part of the conversation). Arguments is the raw JSON string Phoenix
// stored — kept verbatim so the LLM debugging the trace sees exactly what
// the original call carried.
type ToolCall struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

// Message is one turn in the LLM conversation captured by the span.
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// Tool is one function/tool definition the model was given to choose from on
// this span — the catalog, not the calls it made (those live on Message.ToolCalls).
// Parsed from attributes.llm.tools[].tool.json_schema. The Description carries
// the load-bearing selection preconditions, so debugging "why did the model
// pick / ignore tool X" is impossible without it. Parameters is the raw JSON
// schema kept verbatim so the LLM reading the trace sees the exact contract.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// TokenCount is the per-span token accounting. CacheRead and Reasoning come
// from the provider's nested prompt_details / completion_details breakdown and
// are omitted when absent (not every provider/model reports them).
type TokenCount struct {
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
	Total      int `json:"total"`
	CacheRead  int `json:"cache_read,omitempty"`
	Reasoning  int `json:"reasoning,omitempty"`
}

// SpanSummary is one row in a list result — enough to decide which span to
// drill into with get_span, without dumping every message.
type SpanSummary struct {
	SpanNodeID    string  `json:"span_node_id"`
	SpanID        string  `json:"span_id,omitempty"`
	Name          string  `json:"name"`
	SpanKind      string  `json:"span_kind,omitempty"`
	Model         string  `json:"model,omitempty"`
	Status        string  `json:"status,omitempty"`
	LatencyMs     float64 `json:"latency_ms,omitempty"`
	TokensTotal   int     `json:"tokens_total,omitempty"`
	TraceID       string  `json:"trace_id,omitempty"`
	StartTime     string  `json:"start_time,omitempty"`
	SystemPreview string  `json:"system_prompt_preview,omitempty"`
	InputPreview  string  `json:"input_preview,omitempty"`
	OutputPreview string  `json:"output_preview,omitempty"`
	Metadata      string  `json:"metadata,omitempty"`
}

// SpanDetail is the full drill-down for one span: the complete message list
// (system prompt, user turns, tool calls), the tool catalog the model could
// choose from, the model's output, usage, the invocation parameters, and the
// span metadata (which pipeline node, request/room/user ids, …).
type SpanDetail struct {
	SpanNodeID       string         `json:"span_node_id"`
	SpanID           string         `json:"span_id,omitempty"`
	Name             string         `json:"name"`
	SpanKind         string         `json:"span_kind,omitempty"`
	Model            string         `json:"model,omitempty"`
	Provider         string         `json:"provider,omitempty"`
	TraceID          string         `json:"trace_id,omitempty"`
	StartTime        string         `json:"start_time,omitempty"`
	Messages         []Message      `json:"messages"`
	Tools            []Tool         `json:"tools,omitempty"`
	Output           string         `json:"output,omitempty"`
	Tokens           TokenCount     `json:"tokens"`
	InvocationParams map[string]any `json:"invocation_parameters,omitempty"`
	Metadata         map[string]any `json:"metadata,omitempty"`
	LatencyMs        float64        `json:"latency_ms,omitempty"`
	Cost             float64        `json:"cost,omitempty"`
}

// ── Validation & helpers ─────────────────────────────────────────────────

// resolveTimeStart returns the operator-supplied ISO timestamp, or a default
// `defaultDays` back from now when the input is empty.
func resolveTimeStart(in string, defaultDays int) string {
	if v := strings.TrimSpace(in); v != "" {
		return v
	}
	return time.Now().UTC().AddDate(0, 0, -defaultDays).Format("2006-01-02T15:04:05.000Z")
}

// appIDFilter builds the metadata filter condition for list_spans_by_app.
// app_id is interpolated into a Phoenix filter expression, so it MUST be
// rejected if it contains quotes — otherwise it could break out of the
// string literal and inject arbitrary filter logic.
func appIDFilter(appID string) (string, error) {
	appID = strings.TrimSpace(appID)
	if appID == "" {
		return "", fmt.Errorf("app_id is required")
	}
	if strings.ContainsAny(appID, "'\"\\") {
		return "", fmt.Errorf("app_id must not contain quotes or backslashes")
	}
	return fmt.Sprintf("metadata['app_id'] == '%s'", appID), nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}

// preview collapses newlines and truncates, for compact list rows.
func preview(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.Join(strings.Fields(s), " ")
	return truncate(s, previewLen)
}

// ── Attribute parsing (nested GraphQL shape) ─────────────────────────────

// parseAttrs decodes the stringified `attributes` blob into a generic map.
// Returns an empty map (never nil) on malformed/empty input so callers can
// navigate without nil checks.
func parseAttrs(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return map[string]any{}
	}
	return m
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func asSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

func getString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	switch v := m[key].(type) {
	case string:
		return v
	case nil:
		return ""
	default:
		// Content is occasionally a structured array (multi-part). Preserve
		// it as JSON rather than dropping it, so debugging still sees it.
		b, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func getInt(m map[string]any, key string) int {
	if m == nil {
		return 0
	}
	switch v := m[key].(type) {
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case int:
		return v
	default:
		return 0
	}
}

// llmBlock returns attrs["llm"] as a map (the nested OpenInference shape).
func llmBlock(attrs map[string]any) map[string]any {
	return asMap(attrs["llm"])
}

// invocationParams returns attrs.llm.invocation_parameters, which Phoenix
// stores as either a nested map or a JSON-encoded string.
func invocationParams(llm map[string]any) map[string]any {
	switch v := llm["invocation_parameters"].(type) {
	case map[string]any:
		return v
	case string:
		var m map[string]any
		if json.Unmarshal([]byte(v), &m) == nil {
			return m
		}
	}
	return map[string]any{}
}

// parseMessages turns a nested input/output_messages slice into []Message,
// pulling tool_calls out of the OpenInference message.tool_calls[].tool_call
// envelope.
func parseMessages(list any) []Message {
	items := asSlice(list)
	if len(items) == 0 {
		return nil
	}
	out := make([]Message, 0, len(items))
	for _, it := range items {
		msg := asMap(asMap(it)["message"])
		if msg == nil {
			continue
		}
		m := Message{
			Role:       getString(msg, "role"),
			Content:    getString(msg, "content"),
			ToolCallID: getString(msg, "tool_call_id"),
		}
		for _, tc := range asSlice(msg["tool_calls"]) {
			call := asMap(asMap(tc)["tool_call"])
			if call == nil {
				continue
			}
			fn := asMap(call["function"])
			m.ToolCalls = append(m.ToolCalls, ToolCall{
				ID:        getString(call, "id"),
				Name:      getString(fn, "name"),
				Arguments: getString(fn, "arguments"),
			})
		}
		out = append(out, m)
	}
	return out
}

// parseTools extracts the tool catalog offered to the model from
// attrs.llm.tools[]. Each element wraps a json_schema blob — stored as either a
// nested map or a JSON-encoded string — in the OpenAI function shape
// {type:"function", function:{name, description, parameters}}.
func parseTools(list any) []Tool {
	items := asSlice(list)
	if len(items) == 0 {
		return nil
	}
	out := make([]Tool, 0, len(items))
	for _, it := range items {
		fn := functionBlock(asMap(it)["json_schema"])
		if fn == nil {
			// Phoenix nests the schema under a "tool" envelope; fall back to it.
			fn = functionBlock(asMap(asMap(it)["tool"])["json_schema"])
		}
		if fn == nil {
			continue
		}
		t := Tool{
			Name:        getString(fn, "name"),
			Description: getString(fn, "description"),
		}
		if p, ok := fn["parameters"]; ok && p != nil {
			if b, err := json.Marshal(p); err == nil {
				t.Parameters = b
			}
		}
		out = append(out, t)
	}
	return out
}

// functionBlock normalizes a tool json_schema (nested map OR JSON-encoded
// string) into its inner `function` object {name, description, parameters}.
// Providers that store name/description at the top level (no `function`
// wrapper) are handled by returning the schema itself.
func functionBlock(js any) map[string]any {
	var schema map[string]any
	switch v := js.(type) {
	case map[string]any:
		schema = v
	case string:
		if json.Unmarshal([]byte(v), &schema) != nil {
			return nil
		}
	}
	if schema == nil {
		return nil
	}
	if fn := asMap(schema["function"]); fn != nil {
		return fn
	}
	return schema
}

// stripKeys returns a shallow copy of m without the listed keys, or nil if the
// result is empty. Used to drop the redundant `tools` array from the
// invocation parameters (already surfaced, typed, on SpanDetail.Tools).
func stripKeys(m map[string]any, keys ...string) map[string]any {
	if len(m) == 0 {
		return nil
	}
	drop := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		drop[k] = struct{}{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if _, skip := drop[k]; skip {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func modelName(llm, inv map[string]any) string {
	if n := getString(llm, "model_name"); n != "" {
		return n
	}
	if n := getString(inv, "azure_deployment"); n != "" {
		return n
	}
	if n := getString(inv, "model"); n != "" {
		return n
	}
	return ""
}

// firstOutputContent returns the first non-empty output message content,
// falling back to the span's raw output value.
func firstOutputContent(outputs []Message, fallback string) string {
	for _, m := range outputs {
		if m.Content != "" {
			return m.Content
		}
	}
	return fallback
}

func firstByRole(msgs []Message, role string) string {
	for _, m := range msgs {
		if m.Role == role {
			return m.Content
		}
	}
	return ""
}

func lastByRole(msgs []Message, role string) string {
	out := ""
	for _, m := range msgs {
		if m.Role == role {
			out = m.Content
		}
	}
	return out
}

// ── Shapers ──────────────────────────────────────────────────────────────

// summarizeSpan condenses one trace span (from the room flow) into a list row.
func summarizeSpan(s wireSpan) SpanSummary {
	attrs := parseAttrs(s.Attributes)
	llm := llmBlock(attrs)
	inMsgs := parseMessages(llm["input_messages"])
	outMsgs := parseMessages(llm["output_messages"])

	out := SpanSummary{
		SpanNodeID:    s.ID,
		SpanID:        s.SpanID,
		Name:          s.Name,
		SpanKind:      s.SpanKind,
		Model:         modelName(llm, invocationParams(llm)),
		Status:        s.StatusCode,
		LatencyMs:     s.LatencyMs,
		TokensTotal:   s.TokenCountTotal,
		StartTime:     s.StartTime,
		SystemPreview: preview(firstByRole(inMsgs, "system")),
		InputPreview:  preview(lastByRole(inMsgs, "user")),
		OutputPreview: preview(firstOutputContent(outMsgs, s.Output.Value)),
	}
	return out
}

// summarizeAppSpan condenses one root span (from the app_id flow). These are
// not necessarily LLM spans, so previews come from the truncated input/output
// values rather than parsed messages.
func summarizeAppSpan(s wireSpan) SpanSummary {
	return SpanSummary{
		SpanNodeID:    s.ID,
		SpanID:        s.SpanID,
		Name:          s.Name,
		SpanKind:      s.SpanKind,
		Status:        s.StatusCode,
		LatencyMs:     s.LatencyMs,
		TokensTotal:   s.TokenCountTotal,
		TraceID:       s.Trace.TraceID,
		StartTime:     s.StartTime,
		InputPreview:  preview(s.Input.Value),
		OutputPreview: preview(s.Output.Value),
		Metadata:      s.Metadata,
	}
}

// buildSpanDetail expands a single span into the full debugging view.
func buildSpanDetail(s wireSpan) SpanDetail {
	attrs := parseAttrs(s.Attributes)
	llm := llmBlock(attrs)
	inv := invocationParams(llm)
	inMsgs := parseMessages(llm["input_messages"])
	outMsgs := parseMessages(llm["output_messages"])

	tc := asMap(llm["token_count"])
	tokens := TokenCount{
		Prompt:     getInt(tc, "prompt"),
		Completion: getInt(tc, "completion"),
		Total:      s.TokenCountTotal,
		CacheRead:  getInt(asMap(tc["prompt_details"]), "cache_read"),
		Reasoning:  getInt(asMap(tc["completion_details"]), "reasoning"),
	}
	if tokens.Total == 0 {
		tokens.Total = getInt(tc, "total")
	}

	return SpanDetail{
		SpanNodeID: s.ID,
		SpanID:     s.SpanID,
		Name:       s.Name,
		SpanKind:   s.SpanKind,
		Model:      modelName(llm, inv),
		Provider:   getString(llm, "provider"),
		TraceID:    s.Trace.TraceID,
		StartTime:  s.StartTime,
		Messages:   inMsgs,
		Tools:      parseTools(llm["tools"]),
		Output:     firstOutputContent(outMsgs, s.Output.Value),
		Tokens:     tokens,
		// `tools` is dropped from the invocation params — it duplicates the
		// typed Tools catalog above and would otherwise bloat the payload.
		InvocationParams: stripKeys(inv, "tools"),
		Metadata:         asMap(attrs["metadata"]),
		LatencyMs:        s.LatencyMs,
		Cost:             s.CostSummary.Total.Cost,
	}
}

// isLLMKind reports whether a span is an LLM span. GraphQL returns spanKind
// lowercase ("llm"); guard against casing drift just in case.
func isLLMKind(kind string) bool {
	return strings.EqualFold(strings.TrimSpace(kind), "llm")
}
