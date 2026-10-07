package mcp

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// ScopedTokens issues short-lived bearer tokens that
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
// # Why they are signed rather than remembered
//
// A grant used to live only in this process's map, which is right up to
// the moment the process is replaced. An agent outlives the daemon that
// spawned it — a graceful upgrade hands the socket to a successor while
// the old process finishes its turns — and the provider reads its MCP
// config exactly once, at spawn, so it cannot be handed a new token
// mid-run. A successor that never issued the token answers 401 to every
// tool call for the rest of that run.
//
// Carrying the map across the handover (SaveHandoff/LoadHandoff) closed
// most of that, but not all of it: measured on 2026-09-22, the successor
// owned the socket at 08:59:08 and only adopted the predecessor's grants
// at 09:00:53 — 105 seconds of 401 for agents that were already running.
// A token minted by the outgoing process after its snapshot, or any
// restart that is not a handover (a crash, a systemd restart), is not
// carried at all.
//
// So the grant travels INSIDE the token: an HS256 statement signed with
// the app's session secret, the same key and the same reasoning as a CLI
// token. Any generation of the daemon can verify one, there is nothing to
// hand over, and there is no window during boot where a valid token is
// not yet known. The map stays for two cases only — grants adopted from a
// predecessor that still minted the old opaque form, and installs with no
// session secret to sign with.
type ScopedTokens struct {
	mu sync.Mutex
	// m holds grants that are NOT self-describing: the opaque form this
	// type used to mint, adopted from a predecessor or issued when there
	// is no secret to sign with.
	m map[string]scopedGrant
	// revoked is the price of a signed token: a statement cannot be taken
	// back, so a finished child's token id is remembered until it would
	// have expired anyway. In memory on purpose — a restart forgetting a
	// revocation costs at most the remainder of a 2h TTL, and persisting
	// it would rebuild the durable state the signature exists to avoid.
	revoked map[string]time.Time
	now     func() time.Time
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
//
// Two hours, not the twelve this used to keep, because a signed token
// cannot be dropped from a map to kill it: the TTL is now the main way one
// stops being useful. A token is minted per SPAWN and a spawned process is
// reaped ~2 minutes after it goes idle, so the window only has to cover a
// single working turn — deploys with a full test gate run well inside it.
const scopedTokenTTL = 2 * time.Hour

// NewScopedTokens builds an empty issuer.
func NewScopedTokens() *ScopedTokens {
	return &ScopedTokens{m: map[string]scopedGrant{}, revoked: map[string]time.Time{}, now: time.Now}
}

// scopedSecret resolves the HS256 signing key. Injected at boot from the
// app's session secret — the same key the CLI tokens use, so rotating it
// invalidates both, which is what rotating a session secret should mean.
//
// Empty is not fatal: an install that has not got one yet falls back to
// the opaque in-memory form, which works exactly as it did before.
var scopedSecret = func() string { return "" }

// SetScopedTokenSecret installs the signing key resolver. Called once at
// boot, next to clitoken.SetSecret.
func SetScopedTokenSecret(f func() string) {
	if f != nil {
		scopedSecret = f
	}
}

// scopedClaims is the grant, written down. Short names because this rides
// in an Authorization header on every single tool call.
type scopedClaims struct {
	UserID     string   `json:"uid"`
	SessionID  string   `json:"sid,omitempty"`
	TagIDs     []string `json:"tg,omitempty"`
	StripAdmin bool     `json:"sa,omitempty"`
	jwt.RegisteredClaims
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
	// Defensive copy: the caller's slice must not be able to mutate a
	// live grant after issuance.
	cp := make([]string, len(tagIDs))
	copy(cp, tagIDs)

	expires := s.now().Add(scopedTokenTTL)
	if key := strings.TrimSpace(scopedSecret()); key != "" {
		tok, err := signScopedToken(key, userID, sessionID, cp, stripAdmin, s.now(), expires)
		if err == nil {
			return tok, nil
		}
		// Signing is arithmetic on values we just built, so a failure here
		// is not a condition the caller can fix — fall through to the
		// opaque form rather than refusing to spawn the agent at all.
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tok := ScopedTokenPrefix + hex.EncodeToString(raw)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[tok] = scopedGrant{
		userID:     userID,
		tagIDs:     cp,
		expires:    expires,
		stripAdmin: stripAdmin,
		sessionID:  sessionID,
	}
	return tok, nil
}

// signScopedToken writes the grant into the token itself.
func signScopedToken(key, userID, sessionID string, tagIDs []string, stripAdmin bool, issued, expires time.Time) (string, error) {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, scopedClaims{
		UserID:     userID,
		SessionID:  sessionID,
		TagIDs:     tagIDs,
		StripAdmin: stripAdmin,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        hex.EncodeToString(raw),
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(issued.UTC()),
			ExpiresAt: jwt.NewNumericDate(expires.UTC()),
		},
	}).SignedString([]byte(key))
	if err != nil {
		return "", err
	}
	return ScopedTokenPrefix + signed, nil
}

// verifyScopedToken reads a signed token back. ok is false for anything
// this process cannot prove it issued: a bad signature, an expiry in the
// past, an id that has been revoked, or a token that is not signed at all
// (the opaque form, which the caller then looks up in the map).
func (s *ScopedTokens) verifyScopedToken(token string) (scopedGrant, bool) {
	body := strings.TrimPrefix(token, ScopedTokenPrefix)
	if body == token || strings.Count(body, ".") != 2 {
		return scopedGrant{}, false
	}
	key := strings.TrimSpace(scopedSecret())
	if key == "" {
		return scopedGrant{}, false
	}
	var c scopedClaims
	_, err := jwt.ParseWithClaims(body, &c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return []byte(key), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithTimeFunc(s.now))
	if err != nil || c.UserID == "" {
		return scopedGrant{}, false
	}
	s.mu.Lock()
	_, gone := s.revoked[c.ID]
	s.mu.Unlock()
	if gone {
		return scopedGrant{}, false
	}
	g := scopedGrant{
		userID:     c.UserID,
		tagIDs:     append([]string(nil), c.TagIDs...),
		stripAdmin: c.StripAdmin,
		sessionID:  c.SessionID,
	}
	if c.ExpiresAt != nil {
		g.expires = c.ExpiresAt.Time
	}
	return g, true
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
	if g, signed := s.verifyScopedToken(token); signed {
		return g.userID, g.tagIDs, g.stripAdmin, true
	}
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
	if g, signed := s.verifyScopedToken(token); signed {
		return g.sessionID, true
	}
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
//
// An opaque grant is deleted. A signed one cannot be — the statement is
// already out there — so its id is remembered until the moment it would
// have expired anyway, and every lookup checks that list first.
func (s *ScopedTokens) Revoke(token string) {
	if token == "" {
		return
	}
	if id, exp, ok := s.scopedTokenID(token); ok {
		s.mu.Lock()
		s.revoked[id] = exp
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, token)
}

// scopedTokenID reads the id and expiry out of a signed token WITHOUT
// checking whether it is currently usable: revoking one that has just
// been revoked, or that expires in a second, must still be recorded
// rather than quietly skipped.
func (s *ScopedTokens) scopedTokenID(token string) (id string, expires time.Time, ok bool) {
	body := strings.TrimPrefix(token, ScopedTokenPrefix)
	if body == token || strings.Count(body, ".") != 2 {
		return "", time.Time{}, false
	}
	key := strings.TrimSpace(scopedSecret())
	if key == "" {
		return "", time.Time{}, false
	}
	var c scopedClaims
	if _, err := jwt.ParseWithClaims(body, &c, func(t *jwt.Token) (any, error) {
		return []byte(key), nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithoutClaimsValidation()); err != nil || c.ID == "" {
		return "", time.Time{}, false
	}
	exp := s.now().Add(scopedTokenTTL)
	if c.ExpiresAt != nil {
		exp = c.ExpiresAt.Time
	}
	return c.ID, exp, true
}

// Sweep drops expired grants, and the revocations that outlived the
// tokens they were holding down. Cheap; safe to call periodically.
func (s *ScopedTokens) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, g := range s.m {
		if now.After(g.expires) {
			delete(s.m, k)
		}
	}
	for id, exp := range s.revoked {
		if now.After(exp) {
			delete(s.revoked, id)
		}
	}
}
