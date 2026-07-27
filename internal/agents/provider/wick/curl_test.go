package wick

import (
	"encoding/json"
	"strings"
	"testing"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

func mustRecordJSON(t *testing.T, rec interactionRecord) []byte {
	t.Helper()
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func sampleRecord() interactionRecord {
	return interactionRecord{
		Seq:     1,
		Kind:    "generate",
		Model:   "gpt-4o",
		ModelID: "m_test123",
		System:  "You are a helpful assistant.",
		Request: []interactionMsg{
			{Role: "user", Text: "hello there"},
		},
	}
}

// TestBuildCurl_OpenAI reconstructs an OpenAI Chat Completions curl with
// the system prompt, the sent message, and a masked API-key placeholder
// — never the real key.
func TestBuildCurl_OpenAI(t *testing.T) {
	rec := sampleRecord()
	m := provider.WickModel{ID: "m_test123", Kind: "openai", Model: "gpt-4o", APIKey: "sk-real-secret-should-never-appear"}
	out, err := BuildCurl(mustRecordJSON(t, rec), m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "https://api.openai.com/v1/chat/completions") {
		t.Errorf("missing endpoint: %s", out)
	}
	if !strings.Contains(out, "hello there") {
		t.Errorf("missing user message: %s", out)
	}
	if !strings.Contains(out, "You are a helpful assistant.") {
		t.Errorf("missing system prompt: %s", out)
	}
	if strings.Contains(out, "sk-real-secret-should-never-appear") {
		t.Fatal("REAL API KEY LEAKED INTO CURL OUTPUT — this must never happen")
	}
	if !strings.Contains(out, "$WICK_MODEL_API_KEY") {
		t.Errorf("expected a placeholder env var for the key, got: %s", out)
	}
}

// TestBuildCurl_OpenRouter uses the same openai_chat wire format but a
// different base URL — confirms the base URL follows the model's kind.
func TestBuildCurl_OpenRouter(t *testing.T) {
	rec := sampleRecord()
	m := provider.WickModel{ID: "m_test123", Kind: "openrouter", Model: "qwen/qwen3-coder", APIKey: "sk-or-secret"}
	out, err := BuildCurl(mustRecordJSON(t, rec), m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "https://openrouter.ai/api/v1/chat/completions") {
		t.Errorf("missing openrouter endpoint: %s", out)
	}
	if strings.Contains(out, "sk-or-secret") {
		t.Fatal("REAL API KEY LEAKED INTO CURL OUTPUT")
	}
}

// TestBuildCurl_Anthropic reconstructs an Anthropic Messages API curl.
func TestBuildCurl_Anthropic(t *testing.T) {
	rec := sampleRecord()
	m := provider.WickModel{ID: "m_test123", Kind: "anthropic", Model: "claude-sonnet-4-5", APIKey: "sk-ant-real-secret"}
	out, err := BuildCurl(mustRecordJSON(t, rec), m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "https://api.anthropic.com/v1/messages") {
		t.Errorf("missing endpoint: %s", out)
	}
	if !strings.Contains(out, "x-api-key") {
		t.Errorf("missing x-api-key header: %s", out)
	}
	if !strings.Contains(out, "anthropic-version") {
		t.Errorf("missing anthropic-version header: %s", out)
	}
	if strings.Contains(out, "sk-ant-real-secret") {
		t.Fatal("REAL API KEY LEAKED INTO CURL OUTPUT")
	}
}

// TestBuildCurl_Gemini reconstructs a Gemini generateContent curl, with
// the model id in the URL path and the key as a query-param placeholder.
func TestBuildCurl_Gemini(t *testing.T) {
	rec := sampleRecord()
	m := provider.WickModel{ID: "m_test123", Kind: "google", Model: "gemini-flash-latest", APIKey: "AIzaRealSecret"}
	out, err := BuildCurl(mustRecordJSON(t, rec), m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "gemini-flash-latest:generateContent") {
		t.Errorf("missing model in URL: %s", out)
	}
	if strings.Contains(out, "AIzaRealSecret") {
		t.Fatal("REAL API KEY LEAKED INTO CURL OUTPUT")
	}
	if !strings.Contains(out, "$WICK_MODEL_API_KEY") {
		t.Errorf("expected placeholder key param, got: %s", out)
	}
}

// TestBuildCurl_CustomBaseURL respects an explicit BaseURL override (the
// "other" provider kind, or a custom gateway) instead of the vendor default.
func TestBuildCurl_CustomBaseURL(t *testing.T) {
	rec := sampleRecord()
	m := provider.WickModel{ID: "m_test123", Kind: "other", Model: "local-model", BaseURL: "https://my-gateway.internal/v1", APIKey: "x"}
	out, err := BuildCurl(mustRecordJSON(t, rec), m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "https://my-gateway.internal/v1/chat/completions") {
		t.Errorf("custom base_url not honored: %s", out)
	}
}

// TestBuildCurl_ToolCallAndResult renders tool_call / tool_result markers
// inline since the exact vendor wire shape isn't preserved in the log.
func TestBuildCurl_ToolCallAndResult(t *testing.T) {
	rec := interactionRecord{
		ModelID: "m_test123",
		Request: []interactionMsg{
			{Role: "user", Text: "run ls"},
			{Role: "model", ToolCall: "shell"},
			{Role: "user", ToolResp: "file1.txt\nfile2.txt"},
		},
	}
	m := provider.WickModel{ID: "m_test123", Kind: "openai", Model: "gpt-4o", APIKey: "x"}
	out, err := BuildCurl(mustRecordJSON(t, rec), m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "tool_call: shell") {
		t.Errorf("missing tool_call marker: %s", out)
	}
	if !strings.Contains(out, "file1.txt") {
		t.Errorf("missing tool_result content: %s", out)
	}
}

// TestBuildCurl_UnsupportedFormat surfaces a clear error instead of
// silently producing a bogus command.
func TestBuildCurl_UnsupportedFormat(t *testing.T) {
	rec := sampleRecord()
	m := provider.WickModel{ID: "m_test123", Kind: "weird", APIFormat: "totally_unknown", Model: "x", APIKey: "x"}
	_, err := BuildCurl(mustRecordJSON(t, rec), m)
	if err == nil {
		t.Fatal("expected an error for an unsupported api format")
	}
}

// TestBuildCurl_MalformedRecord surfaces a parse error rather than panicking.
func TestBuildCurl_MalformedRecord(t *testing.T) {
	m := provider.WickModel{ID: "m_test123", Kind: "openai", Model: "gpt-4o", APIKey: "x"}
	_, err := BuildCurl([]byte("{not valid json"), m)
	if err == nil {
		t.Fatal("expected a parse error for malformed record JSON")
	}
}

// ── curl builder renderers (single-line / raw HTTP / json / bearer) ──

func TestRenderSingleLine_NoContinuation(t *testing.T) {
	m := provider.WickModel{ID: "m1", Kind: "openai", Model: "gpt-4o"}
	req, err := BuildRequest(mustRecordJSON(t, sampleRecord()), m)
	if err != nil {
		t.Fatal(err)
	}
	out := RenderSingleLine(req, "$WICK_MODEL_API_KEY")
	if strings.Contains(out, "\n") {
		t.Errorf("single-line must be one line (no newline / continuation):\n%s", out)
	}
	if !strings.Contains(out, "chat/completions") || !strings.Contains(out, "hello there") {
		t.Errorf("single-line missing endpoint/body: %s", out)
	}
}

func TestRenderRawHTTP_Shape(t *testing.T) {
	m := provider.WickModel{ID: "m1", Kind: "anthropic", Model: "claude-3"}
	req, err := BuildRequest(mustRecordJSON(t, sampleRecord()), m)
	if err != nil {
		t.Fatal(err)
	}
	out := RenderRawHTTP(req, "$WICK_MODEL_API_KEY")
	if !strings.HasPrefix(out, "POST ") {
		t.Errorf("raw HTTP must start with the request line: %s", out)
	}
	if !strings.Contains(out, "Host: ") || !strings.Contains(out, "\n\n") {
		t.Errorf("raw HTTP missing Host or body separator: %s", out)
	}
}

func TestRenderJSONBody(t *testing.T) {
	m := provider.WickModel{ID: "m1", Kind: "openai", Model: "gpt-4o"}
	req, _ := BuildRequest(mustRecordJSON(t, sampleRecord()), m)
	out := RenderJSONBody(req)
	if strings.Contains(out, "curl") || strings.Contains(out, "-H ") {
		t.Errorf("json body must not contain curl/headers: %s", out)
	}
	if !strings.Contains(out, "hello there") {
		t.Errorf("json body missing content: %s", out)
	}
}

func TestBearerInjection(t *testing.T) {
	m := provider.WickModel{ID: "m1", Kind: "openai", Model: "gpt-4o"}
	req, _ := BuildRequest(mustRecordJSON(t, sampleRecord()), m)
	out := RenderSingleLine(req, "sk-my-real-token")
	if !strings.Contains(out, "Bearer sk-my-real-token") {
		t.Errorf("custom bearer not injected: %s", out)
	}
	if strings.Contains(out, "$WICK_MODEL_API_KEY") {
		t.Errorf("placeholder should be gone with a real token: %s", out)
	}
}

func TestGeminiQueryParamAuth(t *testing.T) {
	m := provider.WickModel{ID: "m1", Kind: "gemini", Model: "gemini-1.5-pro"}
	req, err := BuildRequest(mustRecordJSON(t, sampleRecord()), m)
	if err != nil {
		t.Fatal(err)
	}
	out := RenderSingleLine(req, "$WICK_MODEL_API_KEY")
	if !strings.Contains(out, "key=$WICK_MODEL_API_KEY") {
		t.Errorf("gemini key should be a query param: %s", out)
	}
}

// TestSingleQuoteEscaping is the Postman-truncation regression guard: a
// body containing an apostrophe must NOT terminate the -d '...' string
// early. The '\” idiom keeps the whole body inside one shell-quoted arg.
func TestSingleQuoteEscaping(t *testing.T) {
	rec := interactionRecord{
		ModelID: "m1",
		Request: []interactionMsg{{Role: "user", Text: "don't stop, it's fine"}},
	}
	m := provider.WickModel{ID: "m1", Kind: "openai", Model: "gpt-4o"}
	req, err := BuildRequest(mustRecordJSON(t, rec), m)
	if err != nil {
		t.Fatal(err)
	}
	out := RenderSingleLine(req, "$WICK_MODEL_API_KEY")
	// The apostrophes must be escaped as '\'' — a bare ' would close -d.
	if !strings.Contains(out, `'\''`) {
		t.Errorf("apostrophe in body not shell-escaped (would truncate in Postman):\n%s", out)
	}
	// The command must still end with a closing quote (body fully enclosed).
	if !strings.HasSuffix(out, "'") {
		t.Errorf("rendered curl does not end cleanly with a closing quote:\n%s", out)
	}
}
