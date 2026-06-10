package phoenix

import "testing"

// sampleAttrs mirrors the nested OpenInference shape Phoenix returns in the
// stringified `attributes` field over GraphQL: attrs.llm.{model_name,
// token_count, invocation_parameters(JSON string), input_messages[].message,
// output_messages[].message}, with tool calls wrapped in a tool_call envelope.
const sampleAttrs = `{
  "llm": {
    "model_name": "gpt-4o",
    "provider": "openai",
    "token_count": {"prompt": 1100, "completion": 134, "total": 1234},
    "invocation_parameters": "{\"temperature\":0.0,\"azure_deployment\":\"gpt-4o-deploy\"}",
    "input_messages": [
      {"message": {"role": "system", "content": "You are a helpful assistant."}},
      {"message": {"role": "user", "content": "refund saya gagal"}},
      {"message": {"role": "assistant", "tool_calls": [
        {"tool_call": {"id": "call_1", "function": {"name": "get_order", "arguments": "{\"id\":\"123\"}"}}}
      ]}},
      {"message": {"role": "tool", "tool_call_id": "call_1", "content": "{\"status\":\"refunded\"}"}}
    ],
    "output_messages": [
      {"message": {"role": "assistant", "content": "Maaf atas kendalanya, refund sudah diproses."}}
    ]
  }
}`

func sampleSpan() wireSpan {
	s := wireSpan{
		ID:              "U3BhbjozODk2MjI0MQ==",
		SpanID:          "3896224a",
		Name:            "ChatCompletion",
		SpanKind:        "llm",
		StatusCode:      "OK",
		LatencyMs:       820,
		TokenCountTotal: 1234,
		Attributes:      sampleAttrs,
	}
	s.Trace.TraceID = "trace-abc"
	s.CostSummary.Total.Cost = 0.0042
	return s
}

func TestBuildSpanDetail(t *testing.T) {
	d := buildSpanDetail(sampleSpan())

	if d.Model != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o", d.Model)
	}
	if d.Provider != "openai" {
		t.Errorf("provider = %q, want openai", d.Provider)
	}
	if len(d.Messages) != 4 {
		t.Fatalf("messages len = %d, want 4", len(d.Messages))
	}
	if d.Messages[0].Role != "system" || d.Messages[0].Content != "You are a helpful assistant." {
		t.Errorf("system message mismatch: %+v", d.Messages[0])
	}

	// Tool call parsed out of the OpenInference envelope.
	tc := d.Messages[2].ToolCalls
	if len(tc) != 1 {
		t.Fatalf("assistant tool_calls len = %d, want 1", len(tc))
	}
	if tc[0].Name != "get_order" {
		t.Errorf("tool name = %q, want get_order", tc[0].Name)
	}
	if tc[0].ID != "call_1" || tc[0].Arguments != `{"id":"123"}` {
		t.Errorf("tool call args mismatch: %+v", tc[0])
	}

	// tool_call_id round-trips on the tool message.
	if d.Messages[3].ToolCallID != "call_1" {
		t.Errorf("tool_call_id = %q, want call_1", d.Messages[3].ToolCallID)
	}

	if d.Output != "Maaf atas kendalanya, refund sudah diproses." {
		t.Errorf("output = %q", d.Output)
	}
	if d.Tokens.Prompt != 1100 || d.Tokens.Completion != 134 || d.Tokens.Total != 1234 {
		t.Errorf("tokens = %+v", d.Tokens)
	}
	if d.Cost != 0.0042 {
		t.Errorf("cost = %v, want 0.0042", d.Cost)
	}
}

func TestSummarizeSpan(t *testing.T) {
	s := summarizeSpan(sampleSpan())
	if s.SpanNodeID != "U3BhbjozODk2MjI0MQ==" {
		t.Errorf("span_node_id = %q", s.SpanNodeID)
	}
	if s.Model != "gpt-4o" {
		t.Errorf("model = %q", s.Model)
	}
	if s.SystemPreview != "You are a helpful assistant." {
		t.Errorf("system preview = %q", s.SystemPreview)
	}
	if s.InputPreview != "refund saya gagal" {
		t.Errorf("input preview = %q", s.InputPreview)
	}
	if s.OutputPreview != "Maaf atas kendalanya, refund sudah diproses." {
		t.Errorf("output preview = %q", s.OutputPreview)
	}
}

func TestModelNameFallsBackToAzureDeployment(t *testing.T) {
	llm := map[string]any{
		"invocation_parameters": `{"azure_deployment":"gpt-4o-deploy"}`,
	}
	if got := modelName(llm, invocationParams(llm)); got != "gpt-4o-deploy" {
		t.Errorf("modelName fallback = %q, want gpt-4o-deploy", got)
	}
}

func TestParseAttrsMalformed(t *testing.T) {
	if m := parseAttrs(""); m == nil || len(m) != 0 {
		t.Errorf("empty attrs should yield empty non-nil map, got %v", m)
	}
	if m := parseAttrs("not json"); m == nil || len(m) != 0 {
		t.Errorf("malformed attrs should yield empty non-nil map, got %v", m)
	}
}

func TestAppIDFilter(t *testing.T) {
	got, err := appIDFilter("bibgs-tho4nvlkboaezdf")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := "metadata['app_id'] == 'bibgs-tho4nvlkboaezdf'"; got != want {
		t.Errorf("filter = %q, want %q", got, want)
	}

	if _, err := appIDFilter(""); err == nil {
		t.Error("empty app_id should error")
	}
	for _, bad := range []string{"a'b", `a"b`, `a\b`} {
		if _, err := appIDFilter(bad); err == nil {
			t.Errorf("app_id %q should be rejected (injection guard)", bad)
		}
	}
}

func TestIsLLMKind(t *testing.T) {
	for _, kind := range []string{"llm", "LLM", " llm "} {
		if !isLLMKind(kind) {
			t.Errorf("isLLMKind(%q) = false, want true", kind)
		}
	}
	for _, kind := range []string{"chain", "agent", "tool", ""} {
		if isLLMKind(kind) {
			t.Errorf("isLLMKind(%q) = true, want false", kind)
		}
	}
}
