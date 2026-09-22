package mcp

import (
	"strings"
	"testing"
	"time"
)

// withSecret installs a signing key for the duration of a test, so the
// suite can exercise both forms of token deliberately rather than by
// accident.
func withSecret(t *testing.T, key string) {
	t.Helper()
	prev := scopedSecret
	scopedSecret = func() string { return key }
	t.Cleanup(func() { scopedSecret = prev })
}

// The whole point of signing: the process that verifies a token is not
// the process that issued it. No handoff file, no adoption window — a
// successor that booted seconds ago honours a token minted by a daemon
// that has already exited.
func TestSignedTokenVerifiesInAnotherProcess(t *testing.T) {
	withSecret(t, "session-secret-0123456789")

	old := NewScopedTokens()
	tok, err := old.IssueForSession("user-1", "sess-9", []string{"tag-a", "tag-b"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tok, ScopedTokenPrefix) {
		t.Fatalf("token lost its prefix: %q", tok[:min(12, len(tok))])
	}

	fresh := NewScopedTokens() // a successor: empty map, nothing adopted
	user, tags, strip, ok := fresh.LookupGrant(tok)
	if !ok {
		t.Fatal("a successor rejected a token it did not issue — this is the 401 the signature exists to end")
	}
	if user != "user-1" || strip != true || len(tags) != 2 {
		t.Fatalf("grant came back wrong: %q %v %v", user, tags, strip)
	}
	if sess, ok := fresh.LookupSession(tok); !ok || sess != "sess-9" {
		t.Fatalf("session did not survive: %q %v", sess, ok)
	}
}

// A signed token still stops working when its run is over. The map is
// gone, so revocation is a remembered id — and it has to hold across
// lookups in the same process.
func TestSignedTokenRevoke(t *testing.T) {
	withSecret(t, "session-secret-0123456789")
	s := NewScopedTokens()
	tok, err := s.Issue("user-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := s.Lookup(tok); !ok {
		t.Fatal("fresh token rejected")
	}
	s.Revoke(tok)
	if _, _, ok := s.Lookup(tok); ok {
		t.Fatal("a revoked token still authenticates")
	}
	if _, ok := s.LookupSession(tok); ok {
		t.Fatal("a revoked token still resolves a session")
	}
}

func TestSignedTokenExpires(t *testing.T) {
	withSecret(t, "session-secret-0123456789")
	base := time.Now()
	s := NewScopedTokens()
	s.now = func() time.Time { return base }
	tok, err := s.Issue("user-1", nil)
	if err != nil {
		t.Fatal(err)
	}

	later := NewScopedTokens()
	later.now = func() time.Time { return base.Add(scopedTokenTTL + time.Minute) }
	if _, _, ok := later.Lookup(tok); ok {
		t.Fatal("an expired token was accepted")
	}
}

// A token signed with somebody else's key is not a token. Covers the
// rotated-secret case too: rotating the session secret must invalidate
// outstanding grants, exactly as it does for CLI tokens.
func TestSignedTokenRejectsAnotherKey(t *testing.T) {
	withSecret(t, "session-secret-0123456789")
	tok, err := NewScopedTokens().Issue("user-1", nil)
	if err != nil {
		t.Fatal(err)
	}

	withSecret(t, "a-different-secret-9876543210")
	if _, _, ok := NewScopedTokens().Lookup(tok); ok {
		t.Fatal("a token signed with the previous secret still verifies")
	}
}

func TestSignedTokenRejectsTampering(t *testing.T) {
	withSecret(t, "session-secret-0123456789")
	s := NewScopedTokens()
	tok, err := s.IssueFor("user-1", []string{"tag-a"}, true)
	if err != nil {
		t.Fatal(err)
	}
	// Flip a character in the payload — the signature must stop matching.
	body := []byte(tok)
	i := len(body) / 2
	if body[i] == 'a' {
		body[i] = 'b'
	} else {
		body[i] = 'a'
	}
	if _, _, ok := s.Lookup(string(body)); ok {
		t.Fatal("a tampered token verified")
	}
}

// With no secret configured the type behaves exactly as it did before:
// opaque tokens in a map. An install without a session secret must still
// be able to spawn agents.
func TestWithoutSecretFallsBackToOpaqueTokens(t *testing.T) {
	withSecret(t, "")
	s := NewScopedTokens()
	tok, err := s.IssueForSession("user-1", "sess-1", []string{"tag-a"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(tok, ".") == 2 {
		t.Fatalf("expected the opaque form without a secret, got %q", tok[:min(20, len(tok))])
	}
	if _, _, ok := s.Lookup(tok); !ok {
		t.Fatal("the opaque fallback does not resolve in its own process")
	}
	if _, _, ok := NewScopedTokens().Lookup(tok); ok {
		t.Fatal("an opaque token must NOT resolve elsewhere — that is what the handoff file is for")
	}
}

// Sweep is called periodically; it must not let the revocation list grow
// forever now that it holds ids instead of tokens.
func TestSweepDropsStaleRevocations(t *testing.T) {
	withSecret(t, "session-secret-0123456789")
	base := time.Now()
	s := NewScopedTokens()
	s.now = func() time.Time { return base }
	tok, err := s.Issue("user-1", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.Revoke(tok)
	if len(s.revoked) != 1 {
		t.Fatalf("revocation not recorded: %d", len(s.revoked))
	}
	s.now = func() time.Time { return base.Add(scopedTokenTTL + time.Hour) }
	s.Sweep()
	if len(s.revoked) != 0 {
		t.Fatalf("stale revocation kept: %d", len(s.revoked))
	}
}
