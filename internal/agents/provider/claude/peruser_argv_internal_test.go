package claude

import (
	"strings"
	"testing"
)

// TestFeasibility_ArgvCarriesPerUserToken proves the scoped token reaches the
// spawned CLI: it lands inside the --mcp-config JSON as the Authorization
// header, so swapping the token per spawn swaps the identity the CLI presents.
func TestFeasibility_ArgvCarriesPerUserToken(t *testing.T) {
	argsA := mcpConfigArgs("http://127.0.0.1:9/mcp", "wick_sub_tokenA", "sess-1")
	argsB := mcpConfigArgs("http://127.0.0.1:9/mcp", "wick_sub_tokenB", "sess-1")
	joinA, joinB := strings.Join(argsA, " "), strings.Join(argsB, " ")

	if !strings.Contains(joinA, "Bearer wick_sub_tokenA") {
		t.Fatalf("token A missing from argv: %s", joinA)
	}
	if !strings.Contains(joinB, "Bearer wick_sub_tokenB") {
		t.Fatalf("token B missing from argv: %s", joinB)
	}
	if joinA == joinB {
		t.Fatal("argv identical for different tokens")
	}
	t.Log("PROVEN: per-spawn token lands in --mcp-config Authorization header")
}

// TestArgvNeverIsolatesMCP pins that wick no longer emits
// --strict-mcp-config.
//
// It used to be selectable via WICK_STRICT_MCP, one process-wide value applied
// to every spawn. That stopped making sense once each spawn carries its own
// per-user credential: a single global switch cannot express a per-user
// decision, and it never restricted wick's own tools anyway — it only dropped
// the user's OTHER MCP servers. Access is enforced server-side instead.
func TestArgvNeverIsolatesMCP(t *testing.T) {
	args := strings.Join(mcpConfigArgs("http://127.0.0.1:9/mcp", "tok", "s"), " ")

	if strings.Contains(args, "--strict-mcp-config") {
		t.Fatal("argv isolates MCP; wick merges with the user's own servers")
	}
	// The parts that DO matter are still present.
	if !strings.Contains(args, "Bearer tok") {
		t.Fatal("per-spawn credential missing from argv")
	}
	if !strings.Contains(args, "--mcp-config") {
		t.Fatal("wick server is not injected into the spawn")
	}
}

// TestFeasibility_NoTokenNoMCP confirms the owner-less fallback shape: with an
// empty token no MCP args are emitted at all (no unauthenticated MCP path).
func TestFeasibility_NoTokenNoMCP(t *testing.T) {
	if got := mcpConfigArgs("http://127.0.0.1:9/mcp", "", "s"); got != nil {
		t.Fatalf("empty token produced argv %v, want nil", got)
	}
	t.Log("CONFIRMED: no token -> no MCP wiring; there is no unauthed MCP path")
}
