package mcp

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// subAgentsStubModule mimics the real sub-agents connector (Key
// "sub-agents") with one op, so the MCP layer expands it into top-level
// wick_agent_<op> tools without the real connector's delegation deps.
func subAgentsStubModule() connector.Module {
	type listAgentsInput struct{}
	return connector.Module{
		Meta: connector.Meta{
			Key:         "sub-agents",
			Name:        "Sub-agents",
			Description: "Delegate work to other agents (test stub)",
			Fixed:       true,
		},
		Operations: []connector.Category{
			connector.Cat("", "",
				connector.Op("list_agents", "List Roles", "List delegable roles", listAgentsInput{},
					func(c *connector.Ctx) (any, error) {
						return map[string]any{"agents": []string{}}, nil
					}, wickdocs.Docs{}),
			),
		},
	}
}

// Sub-agents ops surface as top-level wick_agent_<op> tools — the hot
// path of every multi-agent flow must be one call, not the
// wick_get→wick_execute two-hop.
func TestSubAgentsExpandedInCatalog(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, subAgentsStubModule())
	h := NewHandler(svc)

	raw := dispatchJSON(t, h, "tools/list", map[string]any{})
	if !strings.Contains(string(raw), "wick_agent_list_agents") {
		t.Fatalf("tools/list must expand sub-agents ops as top-level tools:\n%s", raw)
	}
	// The shortcut carries an optional session_id so callers on
	// header-less transports can still name their session.
	if !strings.Contains(string(raw), "session_id") {
		t.Fatalf("wick_agent_* schema must include the optional session_id:\n%s", raw)
	}
}

// Unlike wickmanager, sub-agents stays in wick_list too: the connector
// row is the feature's discovery, admin and audit surface, and the
// top-level names are shortcuts — not a replacement.
func TestSubAgentsStillListedInWickList(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, subAgentsStubModule())
	h := NewHandler(svc)

	raw := dispatchJSON(t, h, "tools/call", map[string]any{
		"name":      "wick_list",
		"arguments": map[string]any{},
	})
	if !strings.Contains(string(raw), "Sub-agents") {
		t.Fatalf("sub-agents must still appear in wick_list:\n%s", raw)
	}
}

// A wick_agent_<op> call dispatches through the canonical wick_execute
// path — not "unknown tool".
func TestSubAgentsDispatch(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, subAgentsStubModule())
	h := NewHandler(svc)

	raw := dispatchJSON(t, h, "tools/call", map[string]any{
		"name":      "wick_agent_list_agents",
		"arguments": map[string]any{},
	})
	if strings.Contains(string(raw), "unknown tool") {
		t.Fatalf("wick_agent_list_agents must dispatch:\n%s", raw)
	}
	if !strings.Contains(string(raw), "\"isError\":false") {
		t.Fatalf("wick_agent_list_agents must succeed:\n%s", raw)
	}
}
