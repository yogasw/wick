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

// closedScope permits nothing, as an agent whose Access leaves wickmanager
// off (every non-Captain by default).
type closedScope struct{}

func (closedScope) AllowConnector(string) bool        { return false }
func (closedScope) AllowAccount(string, string) bool  { return false }
func (closedScope) AllowOp(string, string, bool) bool { return false }

// TestWickManagerFollowsAgentScope: wickmanager is a System-tier Access
// entry, so an agent session lists and runs wick_manager_* exactly when
// its scope allows the row — never by a blanket rule either way.
func TestWickManagerFollowsAgentScope(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, wickManagerStubModule())
	h := NewHandler(svc)

	listed := func(scope connectors.AgentScope) bool {
		for _, d := range h.AgentToolDescriptors(connectors.WithAgentScope(context.Background(), scope)) {
			if strings.HasPrefix(d.Name, "wick_manager_") {
				return true
			}
		}
		return false
	}
	if listed(closedScope{}) {
		t.Fatal("a scope without wickmanager must not list wick_manager_*")
	}
	if !listed(openScope{}) {
		t.Fatal("a scope granting wickmanager must list wick_manager_*")
	}

	withScopeResolver(t, func(context.Context, string) connectors.AgentScope { return closedScope{} })
	if out, isErr := h.CallAgentTool(context.Background(), "wick_manager_app_list", map[string]any{}, "sess-agent"); !isErr {
		t.Fatalf("wick_manager_* ran outside the agent's scope: %q", out)
	}
	withScopeResolver(t, func(context.Context, string) connectors.AgentScope { return openScope{} })
	if out, isErr := h.CallAgentTool(context.Background(), "wick_manager_app_list", map[string]any{}, "sess-agent"); isErr {
		t.Fatalf("wick_manager_* refused inside the agent's scope: %q", out)
	}

	// An ordinary session keeps it.
	SetAgentScopeResolver(nil)
	if out, isErr := h.CallAgentTool(context.Background(), "wick_manager_app_list", map[string]any{}, ""); isErr {
		t.Fatalf("ordinary session lost wick_manager_*: %q", out)
	}
}

// featureOffScope permits every connector but switches the schedule tool
// off, as an agent with the Schedule feature disabled does.
type featureOffScope struct{ openScope }

func (featureOffScope) AllowKey(string) bool { return true }
func (featureOffScope) AllowTool(name string) bool {
	return name != "wick_schedule_message"
}

// TestFeatureOffToolHiddenAndRefused: a switched-off feature takes its
// tool out of the list and refuses it at dispatch.
func TestFeatureOffToolHiddenAndRefused(t *testing.T) {
	db := newTestDB(t)
	svc := newTestService(t, db, stubModule())
	h := NewHandler(svc)

	scoped := connectors.WithAgentScope(context.Background(), featureOffScope{})
	for _, d := range h.AgentToolDescriptors(scoped) {
		if d.Name == "wick_schedule_message" {
			t.Fatal("switched-off tool must not be listed")
		}
	}

	withScopeResolver(t, func(context.Context, string) connectors.AgentScope { return featureOffScope{} })
	out, isErr := h.CallAgentTool(context.Background(), "wick_schedule_message", map[string]any{}, "sess-agent")
	if !isErr || !strings.Contains(out, "switched off") {
		t.Fatalf("switched-off tool must be refused, got isErr=%v %q", isErr, out)
	}
}
