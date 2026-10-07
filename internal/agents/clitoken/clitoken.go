// Package clitoken issues the short-lived bearer a SHELL uses to talk back
// into the session that minted it.
//
// # Why this exists
//
// An agent that starts something long — a build, a deploy, a migration —
// has no way to be told how it went. It can poll, or it can schedule a
// wake-up and hope the timing is right; both are guesses about someone
// else's clock. What it actually wants is for the WORK to speak: the build
// script says "0.1.261 built" or "failed at step 3", and the session wakes
// on that.
//
// # What a token is
//
// A signed statement, not a row in a map. It says: this session, this
// user, until this instant — and anything holding the app's session secret
// can check that for itself.
//
// That is not a detail. The moment a script most often reports is the
// moment wick was replaced (a deploy is when builds run), and a credential
// the issuing process had to remember would be refused by its successor —
// losing the report to the very event it was reporting on.
//
// # What keeps it safe
//
//   - It names ONE session, decided when it is minted. The HTTP surface
//     takes no session id at all, so there is no id to swap for somebody
//     else's.
//   - It is minted only over MCP, by an agent already running inside that
//     session. A shell cannot mint one; that is the point, on a host where
//     many people's sessions live side by side.
//   - It is SHORT: thirty minutes by default, two hours at most, clamped
//     here rather than trusted to the caller.
//   - It reaches two endpoints, from this machine only.
//
// There is deliberately no revocation list. A token this narrow and this
// short is not worth the bookkeeping — and the bookkeeping would be a lie
// anyway, since it could not survive the restart it exists to tolerate.
// The lever for "cancel everything now" is rotating the app's session
// secret, which invalidates every outstanding token at once.
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
	"time"

	"github.com/golang-jwt/jwt/v5"
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
	ID        string
	Token     string
	SessionID string
	UserID    string
	Note      string
	ExpiresAt time.Time
}

// claims is what the token says about itself.
type claims struct {
	SessionID string `json:"sid"`
	UserID    string `json:"uid"`
	Note      string `json:"note,omitempty"`
	jwt.RegisteredClaims
}

// secret signs and verifies tokens. Injected at boot from the app's own
// session secret, which already exists, is already protected, and is
// already the thing whose rotation invalidates outstanding credentials.
var secret = func() string { return "" }

// SetSecret installs the signing key resolver. Called once at boot.
func SetSecret(f func() string) {
	if f != nil {
		secret = f
	}
}

// now is the package clock, swapped in tests.
var now = time.Now

// Issue mints a token for sessionID on behalf of userID.
//
// ttl is clamped rather than rejected: a caller asking for a week gets two
// hours and is told so by the returned grant, which is friendlier than an
// error for something with an obvious right answer.
func Issue(sessionID, userID, note string, ttl time.Duration) (Grant, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return Grant{}, errors.New("a CLI token must belong to a session")
	}
	key := strings.TrimSpace(secret())
	if key == "" {
		return Grant{}, errors.New("the app has no session secret to sign with")
	}
	switch {
	case ttl <= 0:
		ttl = DefaultTTL
	case ttl < MinTTL:
		ttl = MinTTL
	case ttl > MaxTTL:
		ttl = MaxTTL
	}

	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return Grant{}, err
	}
	id := hex.EncodeToString(raw)
	issued := now().UTC()
	exp := issued.Add(ttl)

	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{
		SessionID: sessionID,
		UserID:    strings.TrimSpace(userID),
		Note:      strings.TrimSpace(note),
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        id,
			Subject:   sessionID,
			IssuedAt:  jwt.NewNumericDate(issued),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}).SignedString([]byte(key))
	if err != nil {
		return Grant{}, err
	}
	return Grant{
		ID:        id,
		Token:     Prefix + signed,
		SessionID: sessionID,
		UserID:    strings.TrimSpace(userID),
		Note:      strings.TrimSpace(note),
		ExpiresAt: exp,
	}, nil
}

// Resolve verifies a token and returns what it authorises.
//
// Everything in the answer comes from the token itself — signature,
// expiry, the session it names — which is what lets a process honour one
// it never issued.
func Resolve(token string) (Grant, bool) {
	trimmed := strings.TrimSpace(token)
	raw, ok := strings.CutPrefix(trimmed, Prefix)
	if !ok || raw == "" {
		return Grant{}, false // not ours to validate
	}
	key := strings.TrimSpace(secret())
	if key == "" {
		return Grant{}, false
	}
	var c claims
	parsed, err := jwt.ParseWithClaims(raw, &c, func(t *jwt.Token) (any, error) {
		if _, isHMAC := t.Method.(*jwt.SigningMethodHMAC); !isHMAC {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return []byte(key), nil
	},
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithTimeFunc(now),
	)
	if err != nil || !parsed.Valid || c.SessionID == "" || c.ExpiresAt == nil {
		return Grant{}, false
	}
	return Grant{
		ID:        c.ID,
		Token:     trimmed,
		SessionID: c.SessionID,
		UserID:    c.UserID,
		Note:      c.Note,
		ExpiresAt: c.ExpiresAt.Time.UTC(),
	}, true
}

// baseURL reports the URL a script should send to. Injected at boot from
// the app's configured public URL rather than guessed.
var baseURL = func() string { return "" }

// SetBaseURL installs the resolver. Called once at boot.
func SetBaseURL(f func() string) {
	if f != nil {
		baseURL = f
	}
}

// BaseURL is the app's configured address, with a loopback fallback for an
// install that has not set one.
func BaseURL() string {
	if v := strings.TrimRight(strings.TrimSpace(baseURL()), "/"); v != "" {
		return v
	}
	return LoopbackURL()
}

// LoopbackURL is this app's own port on this machine.
func LoopbackURL() string { return "http://127.0.0.1:" + port() }

// localhostURL is the name-based spelling of the same port.
func localhostURL() string { return "http://localhost:" + port() }

func port() string {
	if p := strings.TrimSpace(os.Getenv("WICK_PORT")); p != "" {
		return p
	}
	return "9425"
}

// Candidates are the addresses worth trying, best first.
//
// Loopback only: the channel refuses any request that did not come from
// this machine, so advertising an address that routes through a proxy
// would hand out one that cannot work. Both spellings, because a host
// allowlist can name one and not the other.
//
// Nothing here bypasses that allowlist: an address only wins if it
// actually answers, and loopback answers only when the operator has added
// it (config allowed-origins add http://127.0.0.1:<port>).
func Candidates() []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range []string{LoopbackURL(), localhostURL()} {
		c = strings.TrimRight(strings.TrimSpace(c), "/")
		if c != "" && !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out
}

// PickReachable returns the first candidate that actually answers this
// token, and the reason none did when that happens.
//
// Handing out an address without checking it is how this feature failed
// its first live test: the obvious port answered 403 from the host gate,
// which reads like an auth problem and is not, and the script carrying
// that address had no way to tell the difference. Minting is the right
// moment to find out — it costs one request, and the alternative is a
// build that discovers it at the end, with a result it cannot deliver.
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
		last = fmt.Errorf("%s answered %d: %s", base, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if last == nil {
		last = errors.New("no address to try")
	}
	return "", last
}
