package pool

import "testing"

// TestMCPTokenFor_PerSessionWithFallback pins the credential choice for one
// spawn. A session with an owner gets a per-user token; anything without one
// falls back to the shared internal token rather than losing MCP access.
func TestMCPTokenFor_PerSessionWithFallback(t *testing.T) {
	f := &ClaudeFactory{
		MCPToken: "internal-boot-secret",
		SessionMCPToken: func(sessionID string) (string, bool) {
			switch sessionID {
			case "sess-a":
				return "wick_sub_userA", true
			case "sess-b":
				return "wick_sub_userB", true
			}
			return "", false // no owner: legacy row, cron, system job
		},
	}

	if got := f.mcpTokenFor("sess-a"); got != "wick_sub_userA" {
		t.Fatalf("sess-a token = %q, want per-user token", got)
	}
	if got := f.mcpTokenFor("sess-b"); got != "wick_sub_userB" {
		t.Fatalf("sess-b token = %q, want per-user token", got)
	}
	if f.mcpTokenFor("sess-a") == f.mcpTokenFor("sess-b") {
		t.Fatal("two owners share one credential — identities would collapse")
	}
	// Ownerless / unknown session must still reach MCP.
	if got := f.mcpTokenFor("sess-legacy"); got != "internal-boot-secret" {
		t.Fatalf("ownerless token = %q, want internal fallback", got)
	}
	if got := f.mcpTokenFor(""); got != "internal-boot-secret" {
		t.Fatalf("empty session token = %q, want internal fallback", got)
	}
}

// TestMCPTokenFor_NoMinterKeepsOldBehaviour guards the upgrade path: with the
// minter unwired (or when it returns an empty token), every spawn behaves
// exactly as it did before per-user identity existed.
func TestMCPTokenFor_NoMinterKeepsOldBehaviour(t *testing.T) {
	f := &ClaudeFactory{MCPToken: "internal-boot-secret"}
	if got := f.mcpTokenFor("sess-a"); got != "internal-boot-secret" {
		t.Fatalf("nil minter token = %q, want internal", got)
	}

	// A minter that claims success but yields an empty token must not blank
	// the credential — that would silently drop MCP for that spawn.
	f.SessionMCPToken = func(string) (string, bool) { return "", true }
	if got := f.mcpTokenFor("sess-a"); got != "internal-boot-secret" {
		t.Fatalf("empty-token mint = %q, want internal fallback", got)
	}
}
