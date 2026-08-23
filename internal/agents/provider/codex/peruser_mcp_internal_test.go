package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCodexMCPIsPerUser replaces an earlier guard that asserted codex had NO
// MCP wiring at all. That guard existed to make sure whoever added it built it
// per-user from the start, and it fired when this wiring landed.
//
// What it pins now is the thing that would silently break identity: the spawn
// must take a per-spawn credential and must NOT fall back to the shared
// internal admin token, which is what every wick agent used to authenticate as.
func TestCodexMCPIsPerUser(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(".", "spawn.go"))
	if err != nil {
		t.Fatalf("read spawn.go: %v", err)
	}
	body := string(src)

	if !strings.Contains(body, "MCPToken") {
		t.Fatal("codex spawn no longer carries a per-spawn MCP credential; " +
			"without it a codex agent has no wick tools at all")
	}
	// The credential must be read from the Spawner (minted per session), not
	// from an env var the whole server shares.
	if !strings.Contains(body, "s.MCPToken") {
		t.Error("MCPToken is not read from the Spawner; a shared value cannot " +
			"express a per-user identity")
	}
	// Guard the specific regression: reaching for the process-wide internal
	// token here would make every codex spawn the synthetic admin again.
	for _, banned := range []string{"WICK_MCP_INTERNAL", "internalToken"} {
		if strings.Contains(body, banned) {
			t.Errorf("codex spawn references %q — the per-boot internal token maps "+
				"to a synthetic ADMIN and must never be handed to a spawn", banned)
		}
	}
}
