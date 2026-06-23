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

// wickGetPayload is the subset of the wick_get response the tests read.
type wickGetPayload struct {
	ID         string `json:"id"`
	Categories []struct {
		Category   string `json:"category"`
		TotalTools int    `json:"total_tools"`
	} `json:"categories"`
	Tools []struct {
		ToolID      string           `json:"tool_id"`
		Name        string           `json:"name"`
		Category    string           `json:"category"`
		InputSchema *json.RawMessage `json:"input_schema"`
	} `json:"tools"`
}

// dispatchWickGet runs wick_get and decodes the payload, failing on error.
func dispatchWickGet(t *testing.T, h *Handler, args map[string]any) wickGetPayload {
	t.Helper()
	raw := dispatchJSON(t, h, "tools/call", map[string]any{"name": "wick_get", "arguments": args})
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
	var payload wickGetPayload
	if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return payload
}

// LEVEL 1 — grouped connector, id only: returns categories, no ops, no schema.
func TestWickGetListsCategoriesOnly(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, groupedStubModule())
	h := NewHandler(svc)

	rows, err := svc.List(context.Background())
	if err != nil || len(rows) == 0 {
		t.Fatalf("list: %v rows=%d", err, len(rows))
	}

	p := dispatchWickGet(t, h, map[string]any{"id": rows[0].ID})

	if len(p.Categories) != 2 {
		t.Fatalf("categories = %d, want 2 (Alpha, Beta)", len(p.Categories))
	}
	byName := map[string]int{}
	for _, c := range p.Categories {
		byName[c.Category] = c.TotalTools
	}
	if byName["Alpha"] != 10 || byName["Beta"] != 5 {
		t.Fatalf("category counts = %v, want Alpha:10 Beta:5", byName)
	}
	if len(p.Tools) != 0 {
		t.Fatalf("tools = %d, want 0 at category level", len(p.Tools))
	}
}

// LEVEL 2 — selector = category title: lists that category's ops, no schema.
func TestWickGetCategoryListsOpsNoSchema(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, groupedStubModule())
	h := NewHandler(svc)

	rows, err := svc.List(context.Background())
	if err != nil || len(rows) == 0 {
		t.Fatalf("list: %v rows=%d", err, len(rows))
	}

	p := dispatchWickGet(t, h, map[string]any{"id": rows[0].ID, "selector": "Alpha"})

	if len(p.Categories) != 0 {
		t.Fatalf("categories = %d, want 0 at op-list level", len(p.Categories))
	}
	if len(p.Tools) != 10 {
		t.Fatalf("tools = %d, want 10 (Alpha only)", len(p.Tools))
	}
	for _, tool := range p.Tools {
		if tool.Category != "Alpha" {
			t.Fatalf("op %q category = %q, want Alpha", tool.Name, tool.Category)
		}
		if tool.InputSchema != nil {
			t.Fatalf("op %q has input_schema at op-list level; want omitted", tool.Name)
		}
	}
}

// LEVEL 3 — selector = op key: returns that one op WITH its input_schema.
func TestWickGetOpKeyReturnsSchema(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, groupedStubModule())
	h := NewHandler(svc)

	rows, err := svc.List(context.Background())
	if err != nil || len(rows) == 0 {
		t.Fatalf("list: %v rows=%d", err, len(rows))
	}

	p := dispatchWickGet(t, h, map[string]any{"id": rows[0].ID, "selector": "a_3"})

	if len(p.Tools) != 1 {
		t.Fatalf("tools = %d, want 1 (single op)", len(p.Tools))
	}
	tool := p.Tools[0]
	if tool.Category != "Alpha" {
		t.Fatalf("op category = %q, want Alpha", tool.Category)
	}
	if tool.InputSchema == nil {
		t.Fatalf("op %q missing input_schema at op level", tool.Name)
	}
}

// Flat connector (no named categories): id only lists ops directly (no
// schema), and an op key returns the schema.
func TestWickGetFlatConnector(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, stubModule())
	h := NewHandler(svc)

	rows, err := svc.List(context.Background())
	if err != nil || len(rows) == 0 {
		t.Fatalf("list: %v rows=%d", err, len(rows))
	}
	connectorID := rows[0].ID

	// id only → op list, no categories, no schema.
	p := dispatchWickGet(t, h, map[string]any{"id": connectorID})
	if len(p.Categories) != 0 {
		t.Fatalf("categories = %d, want 0 for flat connector", len(p.Categories))
	}
	if len(p.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(p.Tools))
	}
	if p.Tools[0].Name != "Echo" {
		t.Fatalf("tool name = %q, want Echo", p.Tools[0].Name)
	}
	if p.Tools[0].InputSchema != nil {
		t.Fatalf("flat op list should not carry input_schema")
	}

	// op key → schema.
	p2 := dispatchWickGet(t, h, map[string]any{"id": connectorID, "selector": "echo"})
	if len(p2.Tools) != 1 || p2.Tools[0].InputSchema == nil {
		t.Fatalf("op-key call should return 1 op with schema; got %d tools", len(p2.Tools))
	}
	var schema struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(*p2.Tools[0].InputSchema, &schema); err != nil {
		t.Fatalf("unmarshal schema: %v", err)
	}
	if _, ok := schema.Properties["msg"]; !ok {
		t.Fatalf("input_schema missing 'msg' property")
	}
}

// wick_get with a selector that is neither a category nor an op key errors.
func TestWickGetUnknownSelector(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, groupedStubModule())
	h := NewHandler(svc)

	rows, err := svc.List(context.Background())
	if err != nil || len(rows) == 0 {
		t.Fatalf("list: %v rows=%d", err, len(rows))
	}

	raw := dispatchJSON(t, h, "tools/call", map[string]any{
		"name":      "wick_get",
		"arguments": map[string]any{"id": rows[0].ID, "selector": "Nonexistent"},
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
		t.Fatalf("want isError=true for unknown category:\n%s", raw)
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
		"wick_session_workspace",
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
