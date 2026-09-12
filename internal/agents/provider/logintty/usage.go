package logintty

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/yogasw/wick/internal/agents/provider"
)

// ErrUsageUnsupported marks provider types that expose no usage/limit
// API wick can read yet. codex/gemini support lands in their own files.
var ErrUsageUnsupported = errors.New("usage not supported for this provider type")

// RateLimitedError is the usage endpoint answering 429. It is its own
// type so callers can back off on it specifically instead of retrying
// like they would a timeout — retrying a 429 is what earns the next one.
type RateLimitedError struct {
	// Status is the HTTP status line, kept so the user-facing message
	// reads the same as any other endpoint failure.
	Status string
	// RetryAfter is the server's own Retry-After, zero when absent.
	RetryAfter time.Duration
}

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("usage endpoint: %s", e.Status)
}

// RetryAfterOf reports the server-requested cooldown carried by err,
// or 0 when err is not a rate-limit answer (or carried no header).
func RetryAfterOf(err error) time.Duration {
	var rl *RateLimitedError
	if errors.As(err, &rl) {
		return rl.RetryAfter
	}
	return 0
}

// IsRateLimited reports whether err is the usage endpoint refusing on
// rate-limit grounds.
func IsRateLimited(err error) bool {
	var rl *RateLimitedError
	return errors.As(err, &rl)
}

// parseRetryAfter reads the Retry-After header in either form the spec
// allows: delay-seconds, or an HTTP date. Unparseable or past values
// give 0, meaning "no server guidance — use our own backoff".
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if ts, err := http.ParseTime(v); err == nil {
		if d := time.Until(ts); d > 0 {
			return d
		}
	}
	return 0
}

// UsageWindow is one rate-limit window's utilization (e.g. claude's
// rolling 5-hour and 7-day windows).
type UsageWindow struct {
	Key         string    `json:"key"`
	Utilization float64   `json:"utilization"` // percent, 0-100
	ResetsAt    time.Time `json:"resets_at,omitempty"`
}

// SupportsUsage reports whether this provider type has a usage API wick
// can read. Callers use it to skip the probe machinery entirely for
// types that would only answer ErrUsageUnsupported — no cache entry, no
// pacing slot, no goroutine for a verdict that is a build constant.
func SupportsUsage(t provider.Type) bool {
	return t == provider.TypeClaude
}

// ReadUsage fetches the current rate-limit utilization for one
// instance using its stored credentials. Only claude today.
func ReadUsage(t provider.Type, env []string) ([]UsageWindow, error) {
	switch t {
	case provider.TypeClaude:
		return readClaudeUsage(env)
	default:
		return nil, ErrUsageUnsupported
	}
}

// UsageIdentity names the ACCOUNT behind an instance, so callers can
// probe once per account rather than once per instance.
//
// The upstream rate limit applies to the login, not to the folder it
// sits in: several instances routinely point at one credential dir
// (same account, different flags), and a copied dir is still the same
// account. Two instances sharing an identity must share one probe.
//
// The key is only ever allowed to MERGE instances that are provably the
// same login — a readable email, or an identical access token. When
// neither is readable the config dir is the key, which can only ever
// split (an extra request), never wrongly merge two accounts into one.
func UsageIdentity(t provider.Type, env []string) string {
	switch t {
	case provider.TypeClaude:
		return claudeUsageIdentity(env)
	default:
		return string(t) + ":dir:" + ConfigDir(t, env)
	}
}
