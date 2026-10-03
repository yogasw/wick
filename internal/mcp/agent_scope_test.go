package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/connectors"
)

// openScope is an agent scope that permits every connector, account and
// op — the widest checklist an agent could carry.
type openScope struct{}

func (openScope) AllowConnector(string) bool        { return true }
func (openScope) AllowAccount(string, string) bool  { return true }
func (openScope) AllowOp(string, string, bool) bool { return true }

// withScopeResolver installs f for the test and restores "no scoping".
func withScopeResolver(t *testing.T, f AgentScopeResolver) {
	t.Helper()
	SetAgentScopeResolver(f)
	t.Cleanup(func() { SetAgentScopeResolver(nil) })
}

// TestWickManagerDeniedToAgentSessions: wickmanager is no checklist entry,
// so even a scope that permits everything must not list or run its ops.
func TestWickManagerDeniedToAgentSessions(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, wickManagerStubModule())
	h := NewHandler(svc)

	scoped := connectors.WithAgentScope(context.Background(), openScope{})
	for _, d := range h.AgentToolDescriptors(scoped) {
		if strings.HasPrefix(d.Name, "wick_manager_") {
			t.Fatalf("agent session must not list %s", d.Name)
		}
	}

	withScopeResolver(t, func(context.Context, string) connectors.AgentScope { return openScope{} })
	out, isErr := h.CallAgentTool(context.Background(), "wick_manager_app_list", map[string]any{}, "sess-agent")
	if !isErr || !strings.Contains(out, "not available to agent sessions") {
		t.Fatalf("wick_manager_* must be refused in an agent session, got isErr=%v %q", isErr, out)
	}

	// An ordinary session keeps it.
	out, isErr = h.CallAgentTool(context.Background(), "wick_manager_app_list", map[string]any{}, "")
	if isErr {
		t.Fatalf("ordinary session lost wick_manager_*: %q", out)
	}
}
