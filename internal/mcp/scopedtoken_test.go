package mcp

import (
	"strings"
	"testing"
	"time"
)

func TestScopedTokenRoundTrip(t *testing.T) {
	s := NewScopedTokens()
	tok, err := s.Issue("user-1", []string{"a", "b"})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// The prefix keeps these off the PAT/OAuth validation paths.
	if !strings.HasPrefix(tok, ScopedTokenPrefix) {
		t.Fatalf("token %q lacks the scoped prefix", tok)
	}

	userID, tags, ok := s.Lookup(tok)
	if !ok || userID != "user-1" {
		t.Fatalf("lookup: user=%q ok=%v", userID, ok)
	}
	if len(tags) != 2 || tags[0] != "a" || tags[1] != "b" {
		t.Fatalf("tags = %v, want [a b]", tags)
	}
}

func TestScopedTokenUnknownAndRevoked(t *testing.T) {
	s := NewScopedTokens()
	if _, _, ok := s.Lookup("wick_sub_nope"); ok {
		t.Fatal("unknown token resolved")
	}

	tok, _ := s.Issue("user-1", nil)
	s.Revoke(tok)
	if _, _, ok := s.Lookup(tok); ok {
		t.Fatal("revoked token still resolves; a finished sub-agent could keep calling tools")
	}
}

func TestScopedTokenExpires(t *testing.T) {
	s := NewScopedTokens()
	now := time.Now()
	s.now = func() time.Time { return now }

	tok, _ := s.Issue("user-1", []string{"a"})
	if _, _, ok := s.Lookup(tok); !ok {
		t.Fatal("fresh token should resolve")
	}

	now = now.Add(scopedTokenTTL + time.Minute)
	if _, _, ok := s.Lookup(tok); ok {
		t.Fatal("expired token still resolves")
	}
}

// Grants must not alias the caller's slice: mutating the input after
// issuance must not be able to widen a live grant's tag set.
func TestScopedTokenDoesNotAliasCallerSlice(t *testing.T) {
	s := NewScopedTokens()
	tags := []string{"a"}
	tok, _ := s.Issue("user-1", tags)

	tags[0] = "admin-everything"

	_, got, ok := s.Lookup(tok)
	if !ok {
		t.Fatal("lookup failed")
	}
	if got[0] != "a" {
		t.Fatalf("grant mutated through the caller's slice: %v", got)
	}

	// And the returned copy must not be writable back into the store.
	got[0] = "tampered"
	_, again, _ := s.Lookup(tok)
	if again[0] != "a" {
		t.Fatalf("grant mutated through a returned slice: %v", again)
	}
}

func TestScopedTokensAreUnique(t *testing.T) {
	s := NewScopedTokens()
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok, err := s.Issue("user-1", nil)
		if err != nil {
			t.Fatalf("issue: %v", err)
		}
		if seen[tok] {
			t.Fatalf("duplicate token %q", tok)
		}
		seen[tok] = true
	}
}
