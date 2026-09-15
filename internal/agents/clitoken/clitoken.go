// Package clitoken issues the short-lived bearer a SHELL uses to talk back
// into the session that minted it.
//
// # Why this exists
//
// An agent that starts something long — a build, a deploy, a migration —
// has no way to be told how it went. It can poll, or it can schedule a
// wake-up and hope the timing is right; both are guesses about someone
// else's clock. What it actually wants is for the WORK to speak: the build
// script says "0.1.255 built" or "failed at step 3", and the session wakes
// on that.
//
// That needs a credential a script can carry, and the credential is the
// whole risk. So this one is deliberately the narrowest thing that works:
//
//   - It is bound to ONE session, decided when it is minted. The HTTP
//     surface takes no session id at all — the token IS the session — so
//     there is no id to swap for somebody else's.
//   - It is minted only over MCP, by an agent already running inside that
//     session. A shell cannot mint one; that is the point. Anyone with a
//     session id could otherwise mint a token for a session they do not
//     own, on a host where sessions of many people live side by side.
//   - It expires (30 minutes by default, two hours at most) and lives in
//     memory, so a leaked one is worthless shortly after the build it was
//     cut for, and a restart invalidates every outstanding token.
//   - It carries the minting user, so everything the script does is
//     attributed to a person rather than to a synthetic principal.
package clitoken

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// Prefix marks tokens minted here, distinct from every other bearer wick
// accepts so a resolver can never route one to the wrong validator.
const Prefix = "wick_cli_"

// TTL bounds. The default is sized for a build; the ceiling exists because
// "just make it long" is how a build credential becomes a standing one.
const (
	DefaultTTL = 30 * time.Minute
	MaxTTL     = 2 * time.Hour
	MinTTL     = time.Minute
)

// Grant is what a token authorises: one session, one user, until a time.
type Grant struct {
	Token     string
	SessionID string
	UserID    string
	Note      string
	ExpiresAt time.Time
}

// Store holds live grants. The zero value is not usable; call New.
type Store struct {
	mu  sync.Mutex
	m   map[string]Grant
	now func() time.Time
}

// New returns an empty store.
func New() *Store { return &Store{m: map[string]Grant{}, now: time.Now} }

// Issue mints a token for sessionID on behalf of userID.
//
// ttl is clamped rather than rejected: a caller asking for a week gets two
// hours and is told so by the returned grant, which is friendlier than an
// error for something with an obvious right answer.
func (s *Store) Issue(sessionID, userID, note string, ttl time.Duration) (Grant, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return Grant{}, errors.New("a CLI token must belong to a session")
	}
	switch {
	case ttl <= 0:
		ttl = DefaultTTL
	case ttl < MinTTL:
		ttl = MinTTL
	case ttl > MaxTTL:
		ttl = MaxTTL
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return Grant{}, err
	}
	g := Grant{
		Token:     Prefix + hex.EncodeToString(raw),
		SessionID: sessionID,
		UserID:    strings.TrimSpace(userID),
		Note:      strings.TrimSpace(note),
		ExpiresAt: s.now().Add(ttl).UTC(),
	}
	s.mu.Lock()
	s.sweepLocked()
	s.m[g.Token] = g
	s.mu.Unlock()
	return g, nil
}

// Resolve returns the grant a token authorises, or false when the token is
// unknown or expired. An expired one is dropped on the way out, so the map
// does not accumulate dead grants on a busy host.
func (s *Store) Resolve(token string) (Grant, bool) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, Prefix) {
		return Grant{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.m[token]
	if !ok {
		return Grant{}, false
	}
	if !s.now().Before(g.ExpiresAt) {
		delete(s.m, token)
		return Grant{}, false
	}
	return g, true
}

// Revoke drops one token. Reports whether it was live.
func (s *Store) Revoke(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[strings.TrimSpace(token)]; !ok {
		return false
	}
	delete(s.m, strings.TrimSpace(token))
	return true
}

// ListFor returns the live grants of one session, newest expiry first.
// The token strings are NOT included: a list is for seeing what is
// outstanding, not for recovering a secret somebody lost.
func (s *Store) ListFor(sessionID string) []Grant {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	var out []Grant
	for _, g := range s.m {
		if g.SessionID != sessionID {
			continue
		}
		g.Token = Prefix + "****" + g.Token[len(g.Token)-4:]
		out = append(out, g)
	}
	for i := 1; i < len(out); i++ { // small n; insertion sort keeps it dependency-free
		for j := i; j > 0 && out[j].ExpiresAt.After(out[j-1].ExpiresAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// RevokeSession drops every token of one session and reports how many.
// Called when a session ends: a credential outliving the thing it speaks
// into has no use left, only risk.
func (s *Store) RevokeSession(sessionID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for tok, g := range s.m {
		if g.SessionID == sessionID {
			delete(s.m, tok)
			n++
		}
	}
	return n
}

func (s *Store) sweepLocked() {
	now := s.now()
	for tok, g := range s.m {
		if !now.Before(g.ExpiresAt) {
			delete(s.m, tok)
		}
	}
}

// Default is the process-wide store. One issuer, because the HTTP surface
// and the MCP tool have to agree about what is live.
var Default = New()

// baseURL reports the URL a script should send to. Injected at boot from
// the app's configured public URL rather than guessed: on a host behind a
// proxy the loopback port is NOT the door — it answers 403 from the gate,
// which is a confusing way to learn that the address was wrong.
var baseURL = func() string { return "" }

// SetBaseURL installs the resolver. Called once at boot.
func SetBaseURL(f func() string) {
	if f != nil {
		baseURL = f
	}
}

// BaseURL is the address to hand to a script, with a loopback fallback for
// an install that has not configured one.
func BaseURL() string {
	if v := strings.TrimRight(strings.TrimSpace(baseURL()), "/"); v != "" {
		return v
	}
	return LoopbackURL()
}

// LoopbackURL is this app's own port on this machine.
func LoopbackURL() string {
	return "http://127.0.0.1:" + port()
}

// port is the app's HTTP port.
func port() string {
	if p := strings.TrimSpace(os.Getenv("WICK_PORT")); p != "" {
		return p
	}
	return "9425"
}

// Candidates are the addresses worth trying, best first.
//
// The machine's own port comes FIRST. A build script runs here, so a
// request that leaves for the public name only to be routed back through a
// proxy is a longer path that can fail in more ways — DNS, TLS, the proxy
// itself — for a conversation happening on this host. Both loopback
// spellings are tried because a host allowlist may name one and not the
// other, which is exactly what happened here: localhost was allowed and
// 127.0.0.1 was not.
//
// The configured public URL is the fallback, and it is what makes the
// channel work from a machine that is not this one.
//
// Nothing here bypasses the allowlist: an address only wins if it actually
// answers, and loopback answers only when the operator has allowed it.
func Candidates() []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range []string{LoopbackURL(), localhostURL(), BaseURL()} {
		c = strings.TrimRight(strings.TrimSpace(c), "/")
		if c != "" && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// localhostURL is the name-based spelling of the same port.
func localhostURL() string {
	return "http://localhost:" + port()
}

// PickReachable returns the first candidate that actually answers this
// token, and the reason none did when that happens.
//
// Handing out an address without checking it is how this feature failed
// its first live test: the obvious loopback port answered 403 from the
// host gate, which reads like an auth problem and is not, and the script
// carrying that address had no way to tell the difference. Minting is the
// right moment to find out — it costs one request, and the alternative is
// a build that discovers it at the end, with the result it cannot deliver.
func PickReachable(ctx context.Context, token string, candidates []string) (string, error) {
	var last error
	for _, base := range candidates {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/cli/whoami", nil)
		if err != nil {
			last = err
			continue
		}
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := (&http.Client{Timeout: 4 * time.Second}).Do(req)
		if err != nil {
			last = fmt.Errorf("%s: %w", base, err)
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return base, nil
		}
		last = fmt.Errorf("%s answered %d: %s", base, resp.StatusCode,
			strings.TrimSpace(string(body)))
	}
	if last == nil {
		last = errors.New("no address to try")
	}
	return "", last
}
