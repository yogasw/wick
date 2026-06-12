// Package phoenix wraps the Arize Phoenix observability API as a wick
// connector for debugging LLM behaviour. One instance = one Phoenix project
// (base URL + API token + project id). Operations are read-only: they list
// LLM spans for a Qiscus room or an app_id and drill into a single span's
// full message/tool-call detail — the signals needed to answer "why did the
// agent answer X" without touching or mutating any Phoenix data.
//
// File layout:
//
//   - connector.go — Meta, Configs, Input structs, Operations, thin handlers
//   - service.go   — input validation, attribute parsing, response shaping
//   - repo.go      — outbound GraphQL via http.NewRequestWithContext
package phoenix

import (
	"fmt"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

const Key = "phoenix"

// Configs is the per-instance credential set. ProjectID is the base64 Phoenix
// global id (e.g. "UHJvamVjdDoyOA==" = "Project:28"), copied from the project
// URL or the GraphQL API.
type Configs struct {
	BaseURL   string `wick:"url;required;desc=Phoenix base URL (scheme + host only, no GraphQL path)."`
	APIToken  string `wick:"secret;required;desc=Phoenix API token (JWT). Sent as a Bearer token on every request."`
	ProjectID string `wick:"required;desc=Phoenix project global id, base64 form. Example: UHJvamVjdDoyOA== (decodes to Project:28). Copy it from the project URL or the GraphQL API."`
}

// ListSpansByRoomInput drives list_spans_by_room.
type ListSpansByRoomInput struct {
	RoomID    string `wick:"required;desc=Qiscus room id. Matched against Phoenix session id. Example: 439258020"`
	TimeRange string `wick:"desc=ISO 8601 start time to scope the search, e.g. 2026-03-25T00:00:00.000Z. Default: 365 days ago."`
	LLMOnly   bool   `wick:"checkbox;desc=Return only LLM spans. Default: true. Set false to include chain/agent/tool spans."`
}

// ListSpansByAppInput drives list_spans_by_app.
type ListSpansByAppInput struct {
	AppID     string `wick:"required;desc=Application id stored in span metadata['app_id']. Example: bibgs-tho4nvlkboaezdf"`
	TimeRange string `wick:"desc=ISO 8601 start time. Default: 7 days ago."`
	MaxSpans  int    `wick:"number;desc=Maximum spans to return (paged 100 at a time). Default: 100."`
	RootOnly  bool   `wick:"checkbox;desc=Return only root spans (one per trace). Default: true."`
}

// GetSpanInput drives get_span.
type GetSpanInput struct {
	SpanNodeID string `wick:"required;desc=Phoenix span global id (the span_node_id from a list result). Base64 form, e.g. U3BhbjozODk2MjI0MQ=="`
}

// Meta returns the static metadata block for this connector.
func Meta() connector.Meta {
	return connector.Meta{
		Key:         Key,
		Name:        "Phoenix",
		Description: "Debug LLM behaviour in Arize Phoenix: list LLM spans by room or app_id and inspect a single span's prompt, messages, tool calls, and token usage. Read-only.",
		Icon:        "🔥",
	}
}

// Operations returns the LLM-callable actions for this connector.
func Operations() []connector.Operation {
	return []connector.Operation{
		connector.Op(
			"list_spans_by_room",
			"List Spans by Room",
			"List LLM spans for a Qiscus room (Phoenix session). Returns a summary per span — model, status, tokens, latency, plus previews of the system prompt, last user input, and output. Use this first to find the span to inspect, then call get_span with its span_node_id. Empty result = no spans in range.",
			ListSpansByRoomInput{},
			listSpansByRoom,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"room_id": "Echo of the queried room id.",
					"count":   "Number of spans returned.",
					"spans":   "Array of span summaries (span_node_id, name, model, status, tokens_total, latency_ms, system_prompt_preview, input_preview, output_preview).",
				},
				Quirks: []string{
					"room_id is matched against Phoenix session id, not a metadata filter — a numeric room id cannot be filtered any other way.",
					"Walks sessions -> traces -> spans, so a busy room costs several GraphQL round-trips.",
					"llm_only defaults to true; set it false to also see chain/agent/tool spans.",
					"span_node_id (not span_id) is the handle get_span needs.",
				},
				PairWith:     []string{"connector:phoenix.get_span"},
				InputSample:  `{"room_id":"439258020","llm_only":true}`,
				OutputSample: `{"room_id":"439258020","count":1,"spans":[{"span_node_id":"U3BhbjozODk2MjI0MQ==","name":"ChatCompletion","model":"gpt-4o","status":"OK","tokens_total":1234,"latency_ms":820,"system_prompt_preview":"You are a helpful...","input_preview":"refund saya gagal","output_preview":"Maaf atas kendalanya..."}]}`,
			},
		),
		connector.Op(
			"list_spans_by_app",
			"List Spans by App",
			"List root spans for an application by metadata['app_id']. Returns a summary per span with input/output previews, status, tokens, latency, and trace id. Paginated server-side. Use when you have an app_id rather than a single room.",
			ListSpansByAppInput{},
			listSpansByApp,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"app_id": "Echo of the queried app id.",
					"count":  "Number of spans returned.",
					"spans":  "Array of span summaries (span_node_id, name, span_kind, status, tokens_total, latency_ms, trace_id, input_preview, output_preview, metadata).",
				},
				Quirks: []string{
					"Filters on metadata['app_id'] — only string metadata is filterable in Phoenix.",
					"root_only defaults to true (one span per trace); set false to widen to all spans.",
					"max_spans defaults to 100 and is fetched in pages of 100.",
					"These are root spans, not necessarily LLM spans — previews come from raw input/output, not parsed messages.",
				},
				PairWith:     []string{"connector:phoenix.get_span"},
				InputSample:  `{"app_id":"bibgs-tho4nvlkboaezdf","max_spans":100,"root_only":true}`,
				OutputSample: `{"app_id":"bibgs-tho4nvlkboaezdf","count":1,"spans":[{"span_node_id":"U3Bhbjo0MA==","name":"agent","span_kind":"agent","status":"OK","trace_id":"abc123","input_preview":"...","output_preview":"..."}]}`,
			},
		),
		connector.Op(
			"get_span",
			"Get Span Detail",
			"Fetch the full detail of one span by its span_node_id: every message (system prompt, user turns, tool messages), the catalog of tools the model could choose from, the model output and any tool_calls it requested, token usage, the invocation parameters, the span metadata (pipeline node, request/room/user ids), latency, and cost. This is the core 'why did the AI answer X' view.",
			GetSpanInput{},
			getSpan,
			wickdocs.Docs{
				OutputShape: map[string]string{
					"messages":              "Full conversation: each has role, content, optional tool_calls[] (name + arguments), and tool_call_id.",
					"tools":                 "Catalog of tools the model could choose from: each has name, description (carries the selection preconditions), and parameters (raw JSON schema). Absent if the model was bound no tools. This is what tool_calls were selected against.",
					"output":                "The model's response text for this span.",
					"tokens":                "Object with prompt, completion, total — plus cache_read and reasoning when the provider reports them.",
					"invocation_parameters": "Model call settings (temperature, reasoning_effort, tool_choice, …). The redundant tools array is stripped — see the tools field instead.",
					"metadata":              "Arbitrary span metadata emitted by the application that produced the span — e.g. request id, room/session id, user id, the producing node. Keys vary by producer; useful for cross-referencing logs/DB.",
					"model":                 "Resolved model name (falls back to azure_deployment when llm.model_name is absent).",
				},
				Quirks: []string{
					"Needs the span_node_id (base64 global id) from a list operation, not the hex span_id.",
					"tool_calls are parsed from the OpenInference message.tool_calls[].tool_call envelope; arguments stay as the raw JSON string.",
					"tools (the catalog) is distinct from a message's tool_calls (what the model actually invoked) — compare the two to see whether the model picked the right tool.",
					"tools[].parameters, invocation_parameters, and metadata are passed through verbatim; metadata keys vary by the application that produced the span.",
				},
				PairWith:     []string{"connector:phoenix.list_spans_by_room", "connector:phoenix.list_spans_by_app"},
				InputSample:  `{"span_node_id":"U3BhbjozODk2MjI0MQ=="}`,
				OutputSample: `{"span_node_id":"U3BhbjozODk2MjI0MQ==","name":"ChatCompletion","model":"gpt-4o","messages":[{"role":"system","content":"You are..."},{"role":"user","content":"refund gagal"}],"tools":[{"name":"get_order","description":"Look up an order by id.","parameters":{"type":"object","properties":{"id":{"type":"string"}}}}],"output":"Maaf...","tokens":{"prompt":1100,"completion":134,"total":1234,"cache_read":512},"invocation_parameters":{"temperature":0,"reasoning_effort":"low"},"metadata":{"request_id":"18827eb8-…","room_id":439258020},"latency_ms":820}`,
			},
		),
	}
}

// ── Operation handlers ───────────────────────────────────────────────────

func listSpansByRoom(c *connector.Ctx) (any, error) {
	room := strings.TrimSpace(c.Input("room_id"))
	if room == "" {
		return nil, fmt.Errorf("room_id is required")
	}
	projectID := strings.TrimSpace(c.Cfg("project_id"))
	if projectID == "" {
		return nil, fmt.Errorf("project_id is not configured")
	}
	// llm_only defaults to true when the caller omits it.
	llmOnly := true
	if raw := strings.TrimSpace(c.Input("llm_only")); raw != "" {
		llmOnly = c.InputBool("llm_only")
	}
	timeStart := resolveTimeStart(c.Input("time_range"), 365)

	sessions, err := fetchSessions(c, projectID, room, timeStart)
	if err != nil {
		return nil, fmt.Errorf("fetch sessions: %w", err)
	}

	spans := make([]SpanSummary, 0)
	for _, s := range sessions {
		traces, err := fetchTraces(c, s.ID)
		if err != nil {
			return nil, fmt.Errorf("fetch traces for session %s: %w", s.SessionID, err)
		}
		for _, t := range traces {
			raw, err := fetchTraceSpans(c, t.ID)
			if err != nil {
				return nil, fmt.Errorf("fetch spans for trace %s: %w", t.TraceID, err)
			}
			for _, sp := range raw {
				if llmOnly && !isLLMKind(sp.SpanKind) {
					continue
				}
				spans = append(spans, summarizeSpan(sp))
			}
		}
	}

	return map[string]any{
		"room_id": room,
		"count":   len(spans),
		"spans":   spans,
	}, nil
}

func listSpansByApp(c *connector.Ctx) (any, error) {
	projectID := strings.TrimSpace(c.Cfg("project_id"))
	if projectID == "" {
		return nil, fmt.Errorf("project_id is not configured")
	}
	appID := strings.TrimSpace(c.Input("app_id"))
	filter, err := appIDFilter(appID)
	if err != nil {
		return nil, err
	}
	timeStart := resolveTimeStart(c.Input("time_range"), 7)
	maxSpans := c.InputInt("max_spans")
	if maxSpans <= 0 {
		maxSpans = 100
	}
	rootOnly := true
	if raw := strings.TrimSpace(c.Input("root_only")); raw != "" {
		rootOnly = c.InputBool("root_only")
	}

	raw, err := fetchAppSpans(c, projectID, filter, timeStart, maxSpans, rootOnly)
	if err != nil {
		return nil, fmt.Errorf("fetch app spans: %w", err)
	}
	spans := make([]SpanSummary, 0, len(raw))
	for _, sp := range raw {
		spans = append(spans, summarizeAppSpan(sp))
	}
	return map[string]any{
		"app_id": appID,
		"count":  len(spans),
		"spans":  spans,
	}, nil
}

func getSpan(c *connector.Ctx) (any, error) {
	nodeID := strings.TrimSpace(c.Input("span_node_id"))
	if nodeID == "" {
		return nil, fmt.Errorf("span_node_id is required")
	}
	span, err := fetchSpanDetail(c, nodeID)
	if err != nil {
		return nil, err
	}
	return buildSpanDetail(span), nil
}
