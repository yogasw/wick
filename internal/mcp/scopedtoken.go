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
// # Why this exists
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
	// stripAdmin forces the resolved principal down to RoleUser. True for
	// sub-agents, where the profile's narrowing list is the whole point and
	// an admin parent must not hand its role to a child. False for a
	// top-level session token, where the principal IS the human who is
	// chatting — stripping there would take an admin's own tools away from
	// them for no security gain.
	stripAdmin bool
	// sessionID is the conversation this token was minted for, when the
	// minter knew it. It exists so a tool call can resolve WHICH session
	// it belongs to from the credential itself rather than from an
	// argument the model has to remember to pass.
	//
	// The header X-Wick-Session-Id already does this for claude, but only
	// claude: codex takes no custom headers (it reads the bearer from an
	// env var and nothing else), so anything header-only is provider
	// specific by construction. The token is the one thing every provider
	// carries, so the session rides along with it.
	//
	// Empty is normal and not an error: the shared per-boot token names no
	// session, and a caller that mints before its session exists may leave
	// it blank — the wire header then answers instead.
	sessionID string
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

// Issue mints a sub-agent token bound to userID with exactly tagIDs. The
// principal is demoted to RoleUser (see IssueFor for the top-level case).
//
// The caller is responsible for having already intersected tagIDs down
// to what the user may reach — this type stores what it is given and
// never widens it, but it also cannot verify the intersection for you.
func (s *ScopedTokens) Issue(userID string, tagIDs []string) (string, error) {
	return s.IssueFor(userID, tagIDs, true)
}

// IssueFor mints a token and says explicitly whether the principal should
// be demoted to RoleUser on every request. See scopedGrant.stripAdmin.
func (s *ScopedTokens) IssueFor(userID string, tagIDs []string, stripAdmin bool) (string, error) {
	return s.IssueForSession(userID, "", tagIDs, stripAdmin)
}

// IssueForSession mints a token that also remembers WHICH session it was
// minted for, so calls arriving with it resolve to that conversation
// without the model passing a session_id. sessionID may be empty when the
// minter does not know it yet (sub-agents); the grant is then exactly what
// IssueFor produced before.
func (s *ScopedTokens) IssueForSession(userID, sessionID string, tagIDs []string, stripAdmin bool) (string, error) {
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
	s.m[tok] = scopedGrant{
		userID:     userID,
		tagIDs:     cp,
		expires:    s.now().Add(scopedTokenTTL),
		stripAdmin: stripAdmin,
		sessionID:  sessionID,
	}
	return tok, nil
}

// Lookup resolves a token to its principal. ok is false for unknown or
// expired tokens.
func (s *ScopedTokens) Lookup(token string) (userID string, tagIDs []string, ok bool) {
	userID, tagIDs, _, ok = s.LookupGrant(token)
	return userID, tagIDs, ok
}

// LookupGrant resolves a token and also reports whether the principal must
// be demoted to RoleUser.
func (s *ScopedTokens) LookupGrant(token string) (userID string, tagIDs []string, stripAdmin bool, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, found := s.m[token]
	if !found {
		return "", nil, false, false
	}
	if s.now().After(g.expires) {
		delete(s.m, token)
		return "", nil, false, false
	}
	cp := make([]string, len(g.tagIDs))
	copy(cp, g.tagIDs)
	return g.userID, cp, g.stripAdmin, true
}

// LookupSession resolves the session a token was minted for. ok is false
// for unknown or expired tokens; a valid token minted without a session
// returns ok=true with an empty id, which callers read as "no opinion" and
// fall back to whatever the request itself carries.
func (s *ScopedTokens) LookupSession(token string) (sessionID string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, found := s.m[token]
	if !found {
		return "", false
	}
	if s.now().After(g.expires) {
		delete(s.m, token)
		return "", false
	}
	return g.sessionID, true
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
