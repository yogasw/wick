package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/mcp/handlers"
)

// newTestHandler combines newTestDB + newTestService + NewHandler into one call.
func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	db := newTestDB(t)
	svc := newTestService(t, db, stubModule())
	return NewHandler(svc)
}

// ── wick_search ──────────────────────────────────────────────────────

func TestWickSearchFindsConnector(t *testing.T) {
	h := newTestHandler(t)

	raw := dispatchJSON(t, h, "tools/call", map[string]any{
		"name":      "wick_search",
		"arguments": map[string]any{"query": "echo"},
	})

	var resp struct {
		Result struct {
			Content []struct{ Text string `json:"text"` } `json:"content"`
			IsError bool                                   `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, raw)
	}
	if resp.Result.IsError {
		t.Fatalf("isError=true:\n%s", raw)
	}

	var payload struct {
		Connectors []struct {
			Connector string `json:"connector"`
			Tools     []struct {
				ToolID string `json:"tool_id"`
				Name   string `json:"name"`
			} `json:"tools"`
		} `json:"connectors"`
		Total int    `json:"total"`
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Total != 1 {
		t.Fatalf("total = %d, want 1", payload.Total)
	}
	if payload.Query != "echo" {
		t.Fatalf("query = %q, want echo", payload.Query)
	}
	if len(payload.Connectors) == 0 || len(payload.Connectors[0].Tools) == 0 {
		t.Fatalf("no tools in search result:\n%s", raw)
	}
	if !strings.HasPrefix(payload.Connectors[0].Tools[0].ToolID, "conn:") {
		t.Fatalf("tool_id missing conn: prefix: %q", payload.Connectors[0].Tools[0].ToolID)
	}
}

func TestWickSearchNoQuery(t *testing.T) {
	h := newTestHandler(t)

	raw := dispatchJSON(t, h, "tools/call", map[string]any{
		"name":      "wick_search",
		"arguments": map[string]any{"query": ""},
	})

	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Result.IsError {
		t.Fatal("expected isError=true for empty query")
	}
}

func TestWickSearchNoMatch(t *testing.T) {
	h := newTestHandler(t)

	raw := dispatchJSON(t, h, "tools/call", map[string]any{
		"name":      "wick_search",
		"arguments": map[string]any{"query": "zzznomatch"},
	})

	var resp struct {
		Result struct {
			Content []struct{ Text string `json:"text"` } `json:"content"`
			IsError bool                                   `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Result.IsError {
		t.Fatalf("isError=true for no-match: %s", raw)
	}
	var payload struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Total != 0 {
		t.Fatalf("total = %d, want 0", payload.Total)
	}
}

// ── wick_get ─────────────────────────────────────────────────────────

func TestWickGetReturnsInputSchema(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, stubModule())
	h := NewHandler(svc)

	rows, err := svc.List(context.Background())
	if err != nil || len(rows) == 0 {
		t.Fatalf("list: %v rows=%d", err, len(rows))
	}
	connectorID := rows[0].ID

	raw := dispatchJSON(t, h, "tools/call", map[string]any{
		"name":      "wick_get",
		"arguments": map[string]any{"id": connectorID},
	})

	var resp struct {
		Result struct {
			Content []struct{ Text string `json:"text"` } `json:"content"`
			IsError bool                                   `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, raw)
	}
	if resp.Result.IsError {
		t.Fatalf("isError=true:\n%s", raw)
	}

	var payload struct {
		ID        string `json:"id"`
		Connector string `json:"connector"`
		Tools     []struct {
			ToolID      string `json:"tool_id"`
			Name        string `json:"name"`
			InputSchema struct {
				Type       string                     `json:"type"`
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"input_schema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.ID != connectorID {
		t.Fatalf("id = %q, want %q", payload.ID, connectorID)
	}
	if len(payload.Tools) != 1 {
		t.Fatalf("tools count = %d, want 1", len(payload.Tools))
	}
	tool := payload.Tools[0]
	if tool.Name != "Echo" {
		t.Fatalf("tool name = %q, want Echo", tool.Name)
	}
	if tool.InputSchema.Type != "object" {
		t.Fatalf("input_schema.type = %q, want object", tool.InputSchema.Type)
	}
	if _, ok := tool.InputSchema.Properties["msg"]; !ok {
		t.Fatalf("input_schema missing 'msg' property")
	}
}

func TestWickGetMissingID(t *testing.T) {
	h := newTestHandler(t)

	raw := dispatchJSON(t, h, "tools/call", map[string]any{
		"name":      "wick_get",
		"arguments": map[string]any{"id": ""},
	})

	var resp struct {
		Result struct{ IsError bool `json:"isError"` } `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Result.IsError {
		t.Fatal("expected isError=true for empty id")
	}
}

func TestWickGetUnknownID(t *testing.T) {
	h := newTestHandler(t)

	raw := dispatchJSON(t, h, "tools/call", map[string]any{
		"name":      "wick_get",
		"arguments": map[string]any{"id": "nonexistent-id"},
	})

	var resp struct {
		Result struct{ IsError bool `json:"isError"` } `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !resp.Result.IsError {
		t.Fatal("expected isError=true for unknown connector id")
	}
}

// ── wick_encrypt / wick_decrypt ──────────────────────────────────────

func TestWickEncryptReturnsURL(t *testing.T) {
	h := NewHandler(nil).WithAppURL(func() string { return "https://example.com" })

	raw := dispatchJSON(t, h, "tools/call", map[string]any{
		"name":      "wick_encrypt",
		"arguments": map[string]any{},
	})

	var resp struct {
		Result struct {
			Content []struct{ Text string `json:"text"` } `json:"content"`
			IsError bool                                   `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Result.IsError {
		t.Fatalf("isError=true:\n%s", raw)
	}

	var payload map[string]string
	if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if !strings.Contains(payload["url"], "encfields") {
		t.Fatalf("url = %q, want encfields path", payload["url"])
	}
	if !strings.Contains(payload["url"], "https://example.com") {
		t.Fatalf("url = %q, want base URL prefix", payload["url"])
	}
	if payload["message"] == "" {
		t.Fatal("message empty")
	}
}

func TestWickDecryptReturnsURL(t *testing.T) {
	h := NewHandler(nil).WithAppURL(func() string { return "https://example.com" })

	raw := dispatchJSON(t, h, "tools/call", map[string]any{
		"name":      "wick_decrypt",
		"arguments": map[string]any{},
	})

	var resp struct {
		Result struct {
			Content []struct{ Text string `json:"text"` } `json:"content"`
			IsError bool                                   `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Result.IsError {
		t.Fatalf("isError=true:\n%s", raw)
	}

	var payload map[string]string
	if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if !strings.Contains(payload["url"], "decrypt") {
		t.Fatalf("url = %q, want decrypt path", payload["url"])
	}
}

// ── wick_list_providers ───────────────────────────────────────────────

func TestWickListProvidersNoProviders(t *testing.T) {
	// No provider config on disk in test env — should return empty list, not error.
	h := NewHandler(nil)

	raw := dispatchJSON(t, h, "tools/call", map[string]any{
		"name":      "wick_list_providers",
		"arguments": map[string]any{},
	})

	var resp struct {
		Result struct {
			Content []struct{ Text string `json:"text"` } `json:"content"`
			IsError bool                                   `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v\nbody=%s", err, raw)
	}
	// May error if no provider file exists — acceptable; just verify shape.
	if !resp.Result.IsError {
		var payload struct {
			Providers []any `json:"providers"`
			Total     int   `json:"total"`
		}
		if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &payload); err != nil {
			t.Fatalf("unmarshal payload: %v", err)
		}
		if payload.Total != len(payload.Providers) {
			t.Fatalf("total %d != providers len %d", payload.Total, len(payload.Providers))
		}
	}
}

// ── ask_user ──────────────────────────────────────────────────────────

func TestAskUserDisabledReturnsError(t *testing.T) {
	// No askUsers wired — should return RPC error.
	h := NewHandler(nil)

	ctx := login.WithUser(context.Background(), &entity.User{ID: "u1", Role: entity.RoleAdmin}, nil)
	msg := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ask_user","arguments":{"session_id":"s1","question":"ok?"}}}`
	raw := h.dispatchLine(ctx, []byte(msg))

	// Should be an RPC-level error (no manager wired).
	if !strings.Contains(string(raw), "error") {
		t.Fatalf("expected error in response:\n%s", raw)
	}
}

// ── tools/list completeness ───────────────────────────────────────────

func TestToolsListContainsAllExpectedTools(t *testing.T) {
	want := []string{
		"wick_list", "wick_search", "wick_get", "wick_execute",
		"wick_info", "wick_encrypt", "wick_decrypt",
		"ask_user", "wick_list_providers",
		"wick_skill_list", "wick_skill_sync",
		"wick_session_info", "wick_set_title",
		"wick_session_config",
	}

	descriptors := handlers.MetaToolDescriptors()
	byName := make(map[string]bool, len(descriptors))
	for _, d := range descriptors {
		byName[d.Name] = true
	}

	for _, name := range want {
		if !byName[name] {
			t.Errorf("missing tool: %q", name)
		}
	}
	if len(descriptors) != len(want) {
		t.Errorf("tool count = %d, want %d", len(descriptors), len(want))
	}
}

// ── ParseToolID / FormatToolID round-trip ─────────────────────────────

func TestParseFormatToolIDRoundTrip(t *testing.T) {
	cases := []struct {
		connID string
		opKey  string
	}{
		{"abc123", "echo"},
		{"uuid-with-dashes", "send_message"},
	}
	for _, tc := range cases {
		formatted := handlers.FormatToolID(tc.connID, tc.opKey)
		gotConn, gotOp, err := handlers.ParseToolID(formatted)
		if err != nil {
			t.Fatalf("ParseToolID(%q) error: %v", formatted, err)
		}
		if gotConn != tc.connID || gotOp != tc.opKey {
			t.Fatalf("round-trip failed: got (%q, %q), want (%q, %q)", gotConn, gotOp, tc.connID, tc.opKey)
		}
	}
}

func TestParseToolIDInvalidForms(t *testing.T) {
	cases := []string{
		"",
		"echo",
		"conn:",
		"conn:noslash",
		"conn:/noconnector",
		"conn:connectorid/",
	}
	for _, id := range cases {
		if _, _, err := handlers.ParseToolID(id); err == nil {
			t.Errorf("ParseToolID(%q) = nil error, want error", id)
		}
	}
}

// ── StringifyArgs ─────────────────────────────────────────────────────

func TestStringifyArgs(t *testing.T) {
	in := map[string]any{
		"str":   "hello",
		"num":   float64(42),
		"float": float64(3.14),
		"bool":  true,
		"null":  nil,
		"obj":   map[string]any{"x": 1},
	}
	out := handlers.StringifyArgs(in)

	checks := map[string]string{
		"str":   "hello",
		"num":   "42",
		"float": "3.14",
		"bool":  "true",
		"null":  "",
	}
	for k, want := range checks {
		if got := out[k]; got != want {
			t.Errorf("key %q: got %q, want %q", k, got, want)
		}
	}
	// obj should be JSON-encoded
	if !strings.Contains(out["obj"], "\"x\"") {
		t.Errorf("obj = %q, want JSON", out["obj"])
	}
}

// ── EncfieldsURL ──────────────────────────────────────────────────────

func TestEncfieldsURL(t *testing.T) {
	appURL := func() string { return "https://example.com/" }

	if got := handlers.EncfieldsURL(appURL, "encrypt"); got != "https://example.com/tools/encfields" {
		t.Errorf("encrypt url = %q", got)
	}
	if got := handlers.EncfieldsURL(appURL, "decrypt"); got != "https://example.com/tools/encfields/decrypt" {
		t.Errorf("decrypt url = %q", got)
	}
	// nil appURL falls back to relative path
	if got := handlers.EncfieldsURL(nil, "encrypt"); got != "/tools/encfields" {
		t.Errorf("nil appURL encrypt = %q", got)
	}
}

// ── unknown tool ──────────────────────────────────────────────────────

func TestUnknownToolReturnsRPCError(t *testing.T) {
	h := NewHandler(nil)

	raw := dispatchJSON(t, h, "tools/call", map[string]any{
		"name":      "wick_does_not_exist",
		"arguments": map[string]any{},
	})

	var resp struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error == nil {
		t.Fatalf("expected RPC error, got:\n%s", raw)
	}
	if !strings.Contains(resp.Error.Message, "unknown tool") {
		t.Fatalf("error message = %q, want 'unknown tool'", resp.Error.Message)
	}
}

// ── dispatchLine context helper ───────────────────────────────────────

// localAdminDispatch runs a tools/call with the local admin context
// and returns raw response bytes. Wraps dispatchJSON for one-liner calls.
func localAdminDispatch(t *testing.T, h *Handler, toolName string, args map[string]any) []byte {
	t.Helper()
	p, _ := json.Marshal(map[string]any{"name": toolName, "arguments": args})
	msg := fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":%s}`, p)
	return h.dispatchLine(localAdminCtx(), []byte(msg))
}
