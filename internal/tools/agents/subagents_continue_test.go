package agents

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The route the panel calls and the route the server registers are
// written in two files nothing forces to agree, and a mismatch fails as
// a 404 at click time rather than at build time. Same class of bug the
// status-union test exists for.
func TestContinueRouteMatchesTheFrontEndCall(t *testing.T) {
	fe := filepath.Join("..", "..", "..", "fe", "agents", "conversation", "src", "lib", "api", "subagents.ts")
	src, err := os.ReadFile(fe)
	if err != nil {
		t.Skipf("front-end api not available: %v", err)
	}
	if !regexp.MustCompile(`/api/delegations/\$\{encodeURIComponent\(delegationId\)\}/continue`).Match(src) {
		t.Fatal("the panel does not POST to /api/delegations/{id}/continue — the button cannot reach the server")
	}

	handler, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatalf("read handler.go: %v", err)
	}
	if !strings.Contains(string(handler), `r.POST("/api/delegations/{delegationID}/continue", continueSubAgent)`) {
		t.Fatal("the continue route is not registered — the panel's POST would 404")
	}
}

// A task is the whole point of continuing: it says what changed since
// the sub-agent stopped. Sending it back without one produces the same
// answer twice, so the field must be required at the boundary rather
// than defaulted to something bland.
func TestContinueHandlerRequiresATask(t *testing.T) {
	src, err := os.ReadFile("subagents.go")
	if err != nil {
		t.Fatalf("read subagents.go: %v", err)
	}
	body := string(src)
	start := strings.Index(body, "func continueSubAgent(")
	if start < 0 {
		t.Fatal("continueSubAgent is missing")
	}
	fn := body[start:]
	if end := strings.Index(fn, "\nfunc "); end > 0 {
		fn = fn[:end]
	}

	if !strings.Contains(fn, `strings.TrimSpace(body.Task) == ""`) {
		t.Fatal("continueSubAgent accepts an empty task — the sub-agent would be sent back with no new instruction")
	}
	// Background always: a foreground continue holds the HTTP request open
	// for the whole run, and a browser that gives up looks like a failure
	// while the sub-agent keeps working.
	if !strings.Contains(fn, "delegation.ModeAsync") {
		t.Fatal("continueSubAgent does not run in the background — the request would block for the whole run")
	}
	// resumed is the load-bearing field: it says whether the sub-agent
	// came back with its memory or is starting over in its old session.
	if !strings.Contains(fn, `"resumed"`) {
		t.Fatal("the response omits `resumed` — the UI cannot tell the user their sub-agent forgot its work")
	}
	// A lost race must not read as success. 409 is the signal the panel
	// turns into an error rather than a "continued" toast.
	if !strings.Contains(fn, "ErrNotContinuable") || !strings.Contains(fn, "StatusConflict") {
		t.Fatal("a delegation that started working again must answer 409, not a generic error")
	}
}
