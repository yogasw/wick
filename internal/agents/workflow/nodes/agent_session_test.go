package nodes

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/workflow"
)

func TestResolveAgentSessionID_FallbackToEngineDefault(t *testing.T) {
	rc := newTestRC()
	n := workflow.Node{ID: "ask", Type: workflow.NodeAgent}

	got, err := resolveAgentSessionID(n, rc)
	if err != nil {
		t.Fatalf("resolveAgentSessionID: %v", err)
	}
	want := DefaultRunSessionID(rc.Workflow.ID, rc.RunID)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if err := storage.ValidateSessionID(got); err != nil {
		t.Errorf("storage validator rejected default %q: %v", got, err)
	}
}

func TestResolveAgentSessionID_PrefersDefaultFromSessionInit(t *testing.T) {
	rc := newTestRC()
	rc.DefaultAgentSessionID = "slack-thread-1234"
	n := workflow.Node{ID: "ask", Type: workflow.NodeAgent}

	got, _ := resolveAgentSessionID(n, rc)
	if got != "slack-thread-1234" {
		t.Errorf("got %q, want slack-thread-1234", got)
	}
}

func TestResolveAgentSessionID_SessionNew_AlwaysFresh(t *testing.T) {
	rc := newTestRC()
	rc.DefaultAgentSessionID = "should-be-ignored"
	n := workflow.Node{ID: "ask", Type: workflow.NodeAgent, Session: workflow.SessionNew}

	id1, _ := resolveAgentSessionID(n, rc)
	id2, _ := resolveAgentSessionID(n, rc)
	if id1 == id2 {
		t.Errorf("session=new should produce distinct IDs, both = %q", id1)
	}
	if !strings.HasPrefix(id1, "wf_adhoc_") {
		t.Errorf("expected wf_adhoc_ prefix, got %q", id1)
	}
}

func TestResolveAgentSessionID_SessionFrom_ReusesUpstream(t *testing.T) {
	rc := newTestRC()
	rc.AgentSessionIDs = map[string]string{
		"classify-intent": "wf_it-ops_run_xyz",
	}
	n := workflow.Node{ID: "deep-research", Type: workflow.NodeAgent, SessionFrom: "classify-intent"}

	got, err := resolveAgentSessionID(n, rc)
	if err != nil {
		t.Fatalf("resolveAgentSessionID: %v", err)
	}
	if got != "wf_it-ops_run_xyz" {
		t.Errorf("got %q, want wf_it-ops_run_xyz", got)
	}
}

func TestResolveAgentSessionID_SessionFrom_ForwardRefErrors(t *testing.T) {
	rc := newTestRC() // empty AgentSessionIDs
	n := workflow.Node{ID: "x", Type: workflow.NodeAgent, SessionFrom: "future-node"}

	if _, err := resolveAgentSessionID(n, rc); err == nil {
		t.Fatal("expected error for forward session_from reference")
	}
}

// Resolver order matters: session_from wins over session=new, both
// win over rc.DefaultAgentSessionID, all three win over the engine
// fallback. Validate by stacking conflicting inputs.
func TestResolveAgentSessionID_OrderingHonored(t *testing.T) {
	rc := newTestRC()
	rc.DefaultAgentSessionID = "default-id"
	rc.AgentSessionIDs = map[string]string{"upstream": "upstream-id"}

	// session_from beats session=new beats default.
	n := workflow.Node{ID: "x", SessionFrom: "upstream", Session: workflow.SessionNew}
	if got, _ := resolveAgentSessionID(n, rc); got != "upstream-id" {
		t.Errorf("session_from should win, got %q", got)
	}

	// session=new beats default when session_from empty.
	n2 := workflow.Node{ID: "x", Session: workflow.SessionNew}
	got, _ := resolveAgentSessionID(n2, rc)
	if !strings.HasPrefix(got, "wf_adhoc_") {
		t.Errorf("session=new should win over default, got %q", got)
	}

	// default beats engine fallback when both Session+SessionFrom empty.
	n3 := workflow.Node{ID: "x"}
	if got, _ := resolveAgentSessionID(n3, rc); got != "default-id" {
		t.Errorf("default should win over engine fallback, got %q", got)
	}
}

func TestProviderUsesPool_CaseInsensitive(t *testing.T) {
	if !providerUsesPool("claude") {
		t.Error("claude should route via pool")
	}
	if !providerUsesPool("Claude") {
		t.Error("Claude (capitalized) should route via pool")
	}
	if providerUsesPool("codex") {
		t.Error("codex should not route via pool")
	}
	if providerUsesPool("gemini") {
		t.Error("gemini should not route via pool")
	}
}
