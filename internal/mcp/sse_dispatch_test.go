package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSSEDispatchDelegatesNonStreamingTools guards the catalog/dispatch
// skew that returned "unknown tool: wick_info" over the SSE transport
// (the shared loopback MCP) while it worked over stdio. The SSE path must
// dispatch every non-streaming tool through the canonical switch.
func TestSSEDispatchDelegatesNonStreamingTools(t *testing.T) {
	h := NewHandler(nil)
	req := httptest.NewRequest("POST", "/mcp", nil).WithContext(adminCtx())
	rec := httptest.NewRecorder()
	rpcReq := rpcRequest{
		ID:     json.RawMessage("1"),
		Method: "tools/call",
		Params: json.RawMessage(`{"name":"wick_info","arguments":{}}`),
	}

	h.handleToolsCallSSE(rec, req, rpcReq)

	body := rec.Body.String()
	if strings.Contains(body, "unknown tool") {
		t.Fatalf("wick_info must dispatch over SSE, got unknown-tool error:\n%s", body)
	}
	if !strings.Contains(body, "wick_version") {
		t.Fatalf("expected wick_info payload (wick_version) framed as SSE, got:\n%s", body)
	}
}

// TestSSEDispatchStillServesConnectorTools confirms a tool that previously
// had an explicit SSE case (wick_list) still works after collapsing the
// switch into the canonical dispatcher — the delegation must not regress it.
func TestSSEDispatchStillServesConnectorTools(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, stubModule())
	h := NewHandler(svc)
	req := httptest.NewRequest("POST", "/mcp", nil).WithContext(localAdminCtx())
	rec := httptest.NewRecorder()
	rpcReq := rpcRequest{
		ID:     json.RawMessage("1"),
		Method: "tools/call",
		Params: json.RawMessage(`{"name":"wick_list","arguments":{}}`),
	}

	h.handleToolsCallSSE(rec, req, rpcReq)

	body := rec.Body.String()
	if strings.Contains(body, "unknown tool") {
		t.Fatalf("wick_list must still dispatch over SSE:\n%s", body)
	}
	if !strings.Contains(body, "Stub Connector") {
		t.Fatalf("wick_list over SSE should list the stub connector, got:\n%s", body)
	}
}

// TestSSEDispatchRoutesBatchExecute guards the regression where a "calls"
// batch payload over the SSE transport fell through sseWickExecute's
// single-call path and failed with "tool_id is required" — the batch
// branch (handlers.WickExecute → wickExecuteBatch) was never reached.
// The fix routes a "calls" payload to the canonical handler and frames
// the per-call array as one SSE event.
func TestSSEDispatchRoutesBatchExecute(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, stubModule())
	h := NewHandler(svc)

	rows, err := svc.List(context.Background())
	if err != nil || len(rows) == 0 {
		t.Fatalf("list connectors: %v (rows=%d)", err, len(rows))
	}
	toolID := fmt.Sprintf("conn:%s/echo", rows[0].ID)

	args := map[string]any{
		"calls": []any{
			map[string]any{"tool_id": toolID, "params": map[string]any{"msg": "one"}},
			map[string]any{"tool_id": toolID, "params": map[string]any{"msg": "two"}},
		},
	}
	argsJSON, _ := json.Marshal(args)

	req := httptest.NewRequest("POST", "/mcp", nil).WithContext(localAdminCtx())
	rec := httptest.NewRecorder()
	rpcReq := rpcRequest{
		ID:     json.RawMessage("1"),
		Method: "tools/call",
		Params: json.RawMessage(fmt.Sprintf(`{"name":"wick_execute","arguments":%s}`, argsJSON)),
	}

	h.handleToolsCallSSE(rec, req, rpcReq)

	body := rec.Body.String()
	if strings.Contains(body, "tool_id is required") {
		t.Fatalf("batch payload must not fall through to single-call path:\n%s", body)
	}
	// The batch result array carries ok_count and per-call echoes.
	if !strings.Contains(body, "ok_count") {
		t.Fatalf("expected batch result (ok_count) framed as SSE, got:\n%s", body)
	}
	if !strings.Contains(body, "one") || !strings.Contains(body, "two") {
		t.Fatalf("expected both calls' echoes in batch result, got:\n%s", body)
	}
}
