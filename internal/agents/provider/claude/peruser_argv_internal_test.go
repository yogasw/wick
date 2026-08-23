package claude

import (
	"strings"
	"testing"
)

// TestFeasibility_ArgvCarriesPerUserToken proves the scoped token reaches the
// spawned CLI: it lands inside the --mcp-config JSON as the Authorization
// header, so swapping the token per spawn swaps the identity the CLI presents.
func TestFeasibility_ArgvCarriesPerUserToken(t *testing.T) {
	argsA := mcpConfigArgs("http://127.0.0.1:9/mcp", "wick_sub_tokenA", "sess-1", false)
	argsB := mcpConfigArgs("http://127.0.0.1:9/mcp", "wick_sub_tokenB", "sess-1", false)
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

// TestFeasibility_StrictFlagIsNotACredential shows WICK_STRICT_MCP only toggles
// isolation and carries no identity — it cannot serve as a per-user secret.
func TestFeasibility_StrictFlagIsNotACredential(t *testing.T) {
	loose := strings.Join(mcpConfigArgs("http://127.0.0.1:9/mcp", "tok", "s", false), " ")
	strict := strings.Join(mcpConfigArgs("http://127.0.0.1:9/mcp", "tok", "s", true), " ")

	if strings.Contains(loose, "--strict-mcp-config") {
		t.Fatal("loose mode emitted --strict-mcp-config")
	}
	if !strings.Contains(strict, "--strict-mcp-config") {
		t.Fatal("strict mode missing the flag")
	}
	// Same token both ways: the flag changes isolation, never identity.
	if !strings.Contains(loose, "Bearer tok") || !strings.Contains(strict, "Bearer tok") {
		t.Fatal("token differs between modes")
	}
	t.Log("CONFIRMED: strict flag is isolation-only, not a credential")
}

// TestFeasibility_NoTokenNoMCP confirms the owner-less fallback shape: with an
// empty token no MCP args are emitted at all (no unauthenticated MCP path).
func TestFeasibility_NoTokenNoMCP(t *testing.T) {
	if got := mcpConfigArgs("http://127.0.0.1:9/mcp", "", "s", false); got != nil {
		t.Fatalf("empty token produced argv %v, want nil", got)
	}
	t.Log("CONFIRMED: no token -> no MCP wiring; there is no unauthed MCP path")
}
