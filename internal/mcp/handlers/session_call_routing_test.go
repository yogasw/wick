package handlers

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/sessionworkspace"
)

func TestCallSession_CallWinsOverArgument(t *testing.T) {
	r := httptest.NewRequest("POST", "/mcp", nil)
	r = r.WithContext(WithSessionID(r.Context(), "sess-own"))

	got := CallSession(r, map[string]any{"session_id": "sess-somebody-else"})
	if got != "sess-own" {
		t.Fatalf("CallSession = %q, want sess-own — an argument must not redirect a call", got)
	}

	// With no session on the call, the argument is all there is.
	bare := httptest.NewRequest("POST", "/mcp", nil)
	if got := CallSession(bare, map[string]any{"session_id": "sess-arg"}); got != "sess-arg" {
		t.Fatalf("CallSession = %q, want sess-arg", got)
	}
}

// sessionWith creates a session directory and returns the layout.
func sessionWith(t *testing.T, base string, ids ...string) agentconfig.Layout {
	t.Helper()
	for _, id := range ids {
		if err := os.MkdirAll(filepath.Join(base, "sessions", id), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", id, err)
		}
	}
	return agentconfig.NewLayout(base)
}

// A session-workspace connector holds ITS session's credentials. An agent
// running in another session must not be able to reach it by naming it —
// which is exactly what a session_id argument would be.
func TestSessionInstanceIsNotReachableFromAnotherSession(t *testing.T) {
	layout := sessionWith(t, t.TempDir(), "sess-a", "sess-b")
	inst, err := sessionworkspace.Add(layout, "sess-b", sessionworkspace.Instance{
		BaseKey: "httprest", Label: "B's staging", Config: map[string]string{"base_url": "https://b"},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	// The call runs in sess-a but names sess-b. The argument is ignored, so
	// the instance is looked for in sess-a and is not there.
	r := httptest.NewRequest("POST", "/mcp", nil)
	r = r.WithContext(WithSessionID(r.Context(), "sess-a"))
	args := map[string]any{"session_id": "sess-b"}

	if _, ok, err := SessionInstanceForID(layout, CallSession(r, args), inst.ID); ok || err == nil {
		t.Fatalf("sess-a reached sess-b's connector: ok=%v err=%v", ok, err)
	}

	// Same call from inside sess-b resolves it, so the test above is about
	// the routing and not about a broken fixture.
	rb := httptest.NewRequest("POST", "/mcp", nil)
	rb = rb.WithContext(WithSessionID(rb.Context(), "sess-b"))
	target, ok, err := SessionInstanceForID(layout, CallSession(rb, nil), inst.ID)
	if err != nil || !ok {
		t.Fatalf("resolve from its own session: ok=%v err=%v", ok, err)
	}
	if target.Config["base_url"] != "https://b" {
		t.Fatalf("wrong target: %+v", target)
	}
}

// The session no longer has to be typed: a call that carries one resolves
// its own workspace connectors with no argument at all.
func TestSessionInstanceResolvesWithNoArgument(t *testing.T) {
	layout := sessionWith(t, t.TempDir(), "sess-a")
	inst, err := sessionworkspace.Add(layout, "sess-a", sessionworkspace.Instance{
		BaseKey: "httprest", Label: "Staging", Config: map[string]string{"base_url": "https://x"},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	r := httptest.NewRequest("POST", "/mcp", nil)
	r = r.WithContext(WithSessionID(r.Context(), "sess-a"))

	target, ok, err := SessionInstanceForID(layout, CallSession(r, map[string]any{}), inst.ID)
	if err != nil || !ok {
		t.Fatalf("resolve without session_id: ok=%v err=%v", ok, err)
	}
	if target.BaseKey != "httprest" {
		t.Fatalf("wrong target: %+v", target)
	}
}

// A BATCH entry carries its own session_id, and the entry that omits it
// used to reach the session-instance lookup with an empty string — so a
// batch call to this session's own sw_ connector failed with "session_id
// is required" while the identical single call worked. The resolution now
// happens before the lookup; both shapes see the same session.
//
// The assertion is on WHICH error comes back: "not found in this session"
// means the session was resolved and the instance genuinely is not there,
// where "session_id is required" means it never resolved at all. Both
// return before the connector service is touched, so this needs no service.
func TestExecuteResolvesSessionBeforeTheInstanceLookup(t *testing.T) {
	layout := sessionWith(t, t.TempDir(), "sess-a")

	r := httptest.NewRequest("POST", "/mcp", nil)
	r = r.WithContext(WithSessionID(r.Context(), "sess-a"))

	_, err := executeOneCtx(r.Context(), r, nil, layout,
		"conn:sw_does-not-exist/get", nil, "" /* no session_id on the entry */, nil, nil)
	if err == nil {
		t.Fatal("expected an error for a missing session instance")
	}
	if strings.Contains(err.Error(), "session_id is required") {
		t.Fatalf("session was not resolved before the lookup: %v", err)
	}
	if !strings.Contains(err.Error(), "not found in this session") {
		t.Fatalf("unexpected error: %v", err)
	}
}
