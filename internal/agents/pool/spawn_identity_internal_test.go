package pool

import "testing"

// mintFor records what identity each spawn asked a credential for, so a test
// can assert whose access the process would have carried.
func mintFor(seen *[]string) func(sessionID, callerUserID string) (string, bool) {
	return func(sessionID, callerUserID string) (string, bool) {
		// Stand-in for the real wiring: caller first, session owner when no
		// human triggered the spawn.
		identity := callerUserID
		if identity == "" {
			identity = "owner-of-" + sessionID
		}
		*seen = append(*seen, identity)
		return "token-for-" + identity, true
	}
}

// A spawn runs with the access of the person who ASKED for it.
//
// Before this, the credential was keyed by session alone, so every turn
// carried the owner's reach. On a shared session that is an escalation: a
// Slack thread is multi-user by design, so a teammate replying into an
// admin's thread had their turn served with the admin's connectors — and
// nothing on screen said so.
func TestSpawnRunsAsTheCallerNotTheOwner(t *testing.T) {
	var seen []string
	f := &ClaudeFactory{MCPToken: "internal-token", SessionMCPToken: mintFor(&seen)}

	if got := f.mcpTokenFor("sess-1", "user-b"); got != "token-for-user-b" {
		t.Fatalf("credential = %q, want user-b's own — a caller must not inherit the owner's access", got)
	}
	if len(seen) != 1 || seen[0] != "user-b" {
		t.Fatalf("minted for %v, want [user-b]", seen)
	}
}

// With no human behind the spawn — a schedule fire, a cron job — there is no
// caller to be faithful to, so the session's owner is the right identity.
// This is the path the run-as work depends on: the runner stamps the owner,
// and the spawn picks it up here.
func TestSpawnFallsBackToOwnerWhenNoCaller(t *testing.T) {
	var seen []string
	f := &ClaudeFactory{MCPToken: "internal-token", SessionMCPToken: mintFor(&seen)}

	if got := f.mcpTokenFor("sess-1", ""); got != "token-for-owner-of-sess-1" {
		t.Fatalf("credential = %q, want the session owner's", got)
	}
}

// The caller-change respawn now actually changes the identity. Before, it
// killed the process and minted for the owner again — paying a lost context
// for no change at all, while its own setting promised the new turn would run
// "under that user's own identity and connector access".
func TestRespawnAfterCallerChangeMintsForTheNewCaller(t *testing.T) {
	var seen []string
	f := &ClaudeFactory{MCPToken: "internal-token", SessionMCPToken: mintFor(&seen)}

	// A spawns, then B takes over the same session and the pool recycles.
	_ = f.mcpTokenFor("sess-1", "user-a")
	_ = f.mcpTokenFor("sess-1", "user-b")

	if len(seen) != 2 || seen[0] != "user-a" || seen[1] != "user-b" {
		t.Fatalf("minted for %v, want [user-a user-b] — the respawn must not reuse the first caller", seen)
	}
}
