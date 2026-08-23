package gemini

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGeminiHasNoMCPWiring pins a GAP, not a feature. See the codex
// counterpart for the full rationale: gemini receives no MCP token today, so
// there is no per-user identity to enforce here yet.
//
// It fails the day MCP wiring is added, so whoever adds it makes it per-user
// from the start instead of reintroducing a shared synthetic-admin token.
func TestGeminiHasNoMCPWiring(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(".", "spawn.go"))
	if err != nil {
		t.Fatalf("read spawn.go: %v", err)
	}
	body := string(src)
	for _, marker := range []string{"MCPToken", "--mcp-config", "mcpServers"} {
		if strings.Contains(body, marker) {
			t.Fatalf("gemini spawn.go now references %q — wire it PER-USER "+
				"(mint a per-session token; do not reuse the shared internal "+
				"admin token) and update this test", marker)
		}
	}
}
