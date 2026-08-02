package mcp

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// ScopedTokens issues short-lived, in-memory bearer tokens that
// authenticate a spawned SUB-AGENT to the loopback MCP server as the
// human who triggered it, with an explicitly narrowed tag set.
//
// Why this exists
//
// Normal agent spawns authenticate with the per-boot internal token,
// which maps to a synthetic ADMIN principal. Admin bypasses tag
// filtering entirely. If a sub-agent were handed that same token, a
// profile's "allowed tags" would be decorative: the child would see
// every tool in the system regardless of what the profile said, and
// regardless of what its triggering user was allowed to reach.
//
// So a sub-agent gets one of these instead. The token carries:
//   - the REAL triggering user (so per-owner gates behave normally and
//     the audit trail names a human, not a synthetic admin), and
//   - a precomputed tag slice that is already the intersection of the
//     user's tags with the profile's optional narrowing list.
//
// Tokens are process-local by design. They are only ever sent over
// loopback to this same process, they must die with the run, and
// persisting them would create a durable credential where a transient
// one suffices.
type ScopedTokens struct {
	mu  sync.Mutex
	m   map[string]scopedGrant
	now func() time.Time
}

type scopedGrant struct {
	userID  string
	tagIDs  []string
	expires time.Time
}

// ScopedTokenPrefix marks tokens minted here. Distinct from the PAT
// prefix so resolveToken never routes one to the wrong validator.
const ScopedTokenPrefix = "wick_sub_"

// scopedTokenTTL bounds how long a leaked child token stays useful.
// Generous enough for a long sub-agent run, far short of forever.
const scopedTokenTTL = 12 * time.Hour

// NewScopedTokens builds an empty issuer.
func NewScopedTokens() *ScopedTokens {
	return &ScopedTokens{m: map[string]scopedGrant{}, now: time.Now}
}

// Issue mints a token bound to userID with exactly tagIDs.
//
// The caller is responsible for having already intersected tagIDs down
// to what the user may reach — this type stores what it is given and
// never widens it, but it also cannot verify the intersection for you.
func (s *ScopedTokens) Issue(userID string, tagIDs []string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tok := ScopedTokenPrefix + hex.EncodeToString(raw)

	// Defensive copy: the caller's slice must not be able to mutate a
	// live grant after issuance.
	cp := make([]string, len(tagIDs))
	copy(cp, tagIDs)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[tok] = scopedGrant{userID: userID, tagIDs: cp, expires: s.now().Add(scopedTokenTTL)}
	return tok, nil
}

// Lookup resolves a token to its principal. ok is false for unknown or
// expired tokens.
func (s *ScopedTokens) Lookup(token string) (userID string, tagIDs []string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, found := s.m[token]
	if !found {
		return "", nil, false
	}
	if s.now().After(g.expires) {
		delete(s.m, token)
		return "", nil, false
	}
	cp := make([]string, len(g.tagIDs))
	copy(cp, g.tagIDs)
	return g.userID, cp, true
}

// Revoke drops a token. Called when a delegation reaches a terminal
// state so a finished child cannot keep calling tools.
func (s *ScopedTokens) Revoke(token string) {
	if token == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, token)
}

// Sweep drops expired grants. Cheap; safe to call periodically.
func (s *ScopedTokens) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, g := range s.m {
		if now.After(g.expires) {
			delete(s.m, k)
		}
	}
}
