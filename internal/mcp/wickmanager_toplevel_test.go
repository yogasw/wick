package mcp

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// wickManagerStubModule mimics the real wickmanager connector (Key
// "wickmanager") with one op, so the MCP layer expands it into a
// top-level wick_manager_<op> tool without the real connector's deps.
func wickManagerStubModule() connector.Module {
	type AppListInput struct{}
	return connector.Module{
		Meta: connector.Meta{
			Key:         "wickmanager",
			Name:        "Wick Manager",
			Description: "Manage wick itself (test stub)",
			Fixed:       true,
		},
		Operations: []connector.Operation{
			connector.Op("app_list", "App List", "List app config keys", AppListInput{},
				func(c *connector.Ctx) (any, error) {
					return map[string]any{"ok": true, "keys": []string{}}, nil
				}, wickdocs.Docs{}),
		},
	}
}

// TestWickManagerExpandedInCatalog: wickmanager ops surface as top-level
// wick_manager_<op> tools in tools/list (so the LLM sees them directly).
func TestWickManagerExpandedInCatalog(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, wickManagerStubModule())
	h := NewHandler(svc)

	raw := dispatchJSON(t, h, "tools/list", map[string]any{})
	if !strings.Contains(string(raw), "wick_manager_app_list") {
		t.Fatalf("tools/list must expand wickmanager ops as top-level tools:\n%s", raw)
	}
}

// TestWickManagerNotDuplicatedInMetaTools: wickmanager is surfaced ONLY as
// top-level wick_manager_* tools — it must NOT also appear as a connector in
// the wick_list discovery surface (otherwise its ops are exposed twice).
func TestWickManagerNotDuplicatedInMetaTools(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, wickManagerStubModule())
	h := NewHandler(svc)

	raw := dispatchJSON(t, h, "tools/call", map[string]any{
		"name":      "wick_list",
		"arguments": map[string]any{},
	})
	if strings.Contains(string(raw), "Wick Manager") {
		t.Fatalf("wickmanager must not appear in wick_list (it's top-level only):\n%s", raw)
	}
}

// TestWickManagerDispatchStdio: a wick_manager_<op> call resolves over the
// stdio/JSON path (handleToolsCall) — not "unknown tool".
func TestWickManagerDispatchStdio(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, wickManagerStubModule())
	h := NewHandler(svc)

	raw := dispatchJSON(t, h, "tools/call", map[string]any{
		"name":      "wick_manager_app_list",
		"arguments": map[string]any{},
	})
	if strings.Contains(string(raw), "unknown tool") {
		t.Fatalf("wick_manager_app_list must dispatch over stdio:\n%s", raw)
	}
	if !strings.Contains(string(raw), "\"isError\":false") {
		t.Fatalf("expected a successful op result, got:\n%s", raw)
	}
}

// TestWickManagerDispatchSSE: the same tool also resolves over the SSE
// transport (via the canonical dispatch delegation).
func TestWickManagerDispatchSSE(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, wickManagerStubModule())
	h := NewHandler(svc)

	req := httptest.NewRequest("POST", "/mcp", nil).WithContext(localAdminCtx())
	rec := httptest.NewRecorder()
	rpcReq := rpcRequest{
		ID:     json.RawMessage("1"),
		Method: "tools/call",
		Params: json.RawMessage(`{"name":"wick_manager_app_list","arguments":{}}`),
	}

	h.handleToolsCallSSE(rec, req, rpcReq)

	body := rec.Body.String()
	if strings.Contains(body, "unknown tool") {
		t.Fatalf("wick_manager_app_list must dispatch over SSE:\n%s", body)
	}
	if !strings.Contains(body, "\"isError\":false") {
		t.Fatalf("expected a successful op result over SSE, got:\n%s", body)
	}
}
