package mcp

import (
	"encoding/json"
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
