package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCodexHasNoMCPWiring pins a GAP, not a feature.
//
// Per-user MCP identity was wired for claude (per-session bearer in
// --mcp-config) and for the in-process wick provider (principal passed
// directly). codex has NO MCP wiring at all: it never receives an MCP token,
// so there is nothing here to scope per user yet.
//
// The codex CLI itself does support a streamable-HTTP MCP server with a
// bearer read from an env var (`codex mcp add --url ... --bearer-token-env-var`,
// or -c mcp_servers.<name>.{url,bearer_token_env_var}), so per-user identity is
// reachable the same way once the spawner is wired: mint the per-session token
// and export it as that env var. Verified against codex-cli 0.133.0.
//
// This test fails the day someone adds MCP wiring here, as a reminder to make
// it per-user from the start rather than reintroducing a shared admin token.
func TestCodexHasNoMCPWiring(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(".", "spawn.go"))
	if err != nil {
		t.Fatalf("read spawn.go: %v", err)
	}
	body := string(src)
	for _, marker := range []string{"MCPToken", "--mcp-config", "mcp_servers"} {
		if strings.Contains(body, marker) {
			t.Fatalf("codex spawn.go now references %q — wire it PER-USER "+
				"(mint a per-session token; do not reuse the shared internal "+
				"admin token) and update this test", marker)
		}
	}
}
