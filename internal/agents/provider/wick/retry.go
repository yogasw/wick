package wick

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"
)

// retry.go centralizes the retry+timeout policy for outbound model calls so
// every adapter behaves the same. Before this, only the HTTP adapters
// (OpenAI/Anthropic via postJSON) retried 429/5xx/transport, and only they
// bounded each attempt with a timeout — Gemini had neither. This gives all
// adapters one policy: bounded per-attempt timeout + exponential-backoff retry
// of transient failures (timeout, connection reset, 429, 5xx, "unavailable"),
// while fatal errors (bad key/model, 4xx) fail fast.

// retryPolicy is the tunable retry+timeout policy for one model call. Zero
// values fall back to the defaults, so an unset config still behaves sanely.
type retryPolicy struct {
	maxAttempts int           // total tries incl. the first; <1 → defaultMaxAttempts
	callTimeout time.Duration // per-attempt ceiling; <=0 → defaultCallTimeout
	baseBackoff time.Duration // first backoff, doubled each retry; <=0 → 500ms
}

const (
	defaultMaxAttempts = 3
	defaultCallTimeout = 120 * time.Second
	defaultBaseBackoff = 500 * time.Millisecond
)

func (p retryPolicy) attempts() int {
	if p.maxAttempts < 1 {
		return defaultMaxAttempts
	}
	return p.maxAttempts
}

func (p retryPolicy) timeout() time.Duration {
	if p.callTimeout <= 0 {
		return defaultCallTimeout
	}
	return p.callTimeout
}

func (p retryPolicy) backoff() time.Duration {
	if p.baseBackoff <= 0 {
		return defaultBaseBackoff
	}
	return p.baseBackoff
}

// defaultRetryPolicy is the fallback used when no per-call policy is threaded
// through. It matches the historical HTTP-adapter behavior (3 attempts, 120s).
var defaultRetryPolicy = retryPolicy{}

// retryPolicyFromConfig builds a policy from the resolved wick config. Both
// knobs are optional; unset → defaults. A max of 1 disables retries.
func retryPolicyFromConfig(maxAttempts, callTimeoutSec int) retryPolicy {
	p := retryPolicy{maxAttempts: maxAttempts}
	if callTimeoutSec > 0 {
		p.callTimeout = time.Duration(callTimeoutSec) * time.Second
	}
	return p
}

// retryPolicyKey carries the resolved policy through the context so adapters
// apply the operator's configured retries/timeout without a signature change
// (the engine attaches it around each model call).
type retryPolicyKey struct{}

func withRetryPolicy(ctx context.Context, p retryPolicy) context.Context {
	return context.WithValue(ctx, retryPolicyKey{}, p)
}

// retryPolicyFromContext returns the policy attached by the engine, or the
// default when none is set (headless/test paths).
func retryPolicyFromContext(ctx context.Context) retryPolicy {
	if p, ok := ctx.Value(retryPolicyKey{}).(retryPolicy); ok {
		return p
	}
	return defaultRetryPolicy
}

// isRetryableErr reports whether err is a transient failure worth retrying:
// a context/network timeout, a connection error, or a vendor error string that
// looks like 429 / 5xx / "unavailable" / "overloaded". Fatal errors (401/404,
// invalid key/model) are NOT retryable — retrying them just wastes time. This
// works for both the HTTP adapters and the Gemini SDK (whose errors are opaque
// values, so we fall back to string classification).
func isRetryableErr(err error) bool {
	if err == nil {
		return false
	}
	// A cancelled parent context is not "retryable" — the caller wants to stop.
	if errors.Is(err, context.Canceled) {
		return false
	}
	// Per-attempt deadline is retryable; so is any net timeout.
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	low := strings.ToLower(err.Error())
	// Fatal signals win: never retry an auth/model error even if the message
	// happens to also contain a retryable-looking token.
	for _, fatal := range []string{"401", "unauthorized", "invalid_api_key", "invalid api key", "404", "model_not_found", "does not exist"} {
		if strings.Contains(low, fatal) {
			return false
		}
	}
	for _, r := range []string{
		"429", "rate limit", "ratelimit", "quota",
		"500", "502", "503", "504",
		"unavailable", "overloaded", "server error", "internal error",
		"timeout", "timed out", "deadline",
		"connection reset", "connection refused", "eof", "broken pipe", "no such host",
	} {
		if strings.Contains(low, r) {
			return true
		}
	}
	return false
}

// retryReasonFor gives a short human label for a retryable error, shown in the
// live "retrying (attempt N — reason)" badge.
func retryReasonFor(err error) string {
	if err == nil {
		return ""
	}
	low := strings.ToLower(err.Error())
	switch {
	case strings.Contains(low, "429") || strings.Contains(low, "rate limit") || strings.Contains(low, "quota"):
		return "rate limited"
	case strings.Contains(low, "unavailable") || strings.Contains(low, "overloaded"):
		return "provider unavailable"
	case strings.Contains(low, "500") || strings.Contains(low, "502") || strings.Contains(low, "503") || strings.Contains(low, "504") || strings.Contains(low, "server error"):
		return "server error"
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(low, "timeout") || strings.Contains(low, "timed out") || strings.Contains(low, "deadline"):
		return "timed out"
	default:
		return "connection error"
	}
}

// doWithRetry runs attempt under the policy: each try gets its own timeout-bound
// child context, and a retryable failure backs off and retries up to
// policy.attempts(). notify (optional) is called before each retry so the UI can
// show "retrying (attempt N — reason)". Returns the last error if all attempts
// fail, or ctx.Err() if the parent is cancelled during a backoff.
func doWithRetry(ctx context.Context, policy retryPolicy, notify retryNotify, attempt func(callCtx context.Context) error) error {
	attempts := policy.attempts()
	backoff := policy.backoff()
	var lastErr error
	for i := 0; i < attempts; i++ {
		if i > 0 {
			if notify != nil {
				notify(i+1, retryReasonFor(lastErr))
			}
			select {
			case <-time.After(backoff):
				backoff *= 2
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		callCtx, cancel := context.WithTimeout(ctx, policy.timeout())
		err := attempt(callCtx)
		cancel()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err() // parent cancelled — stop, don't retry
		}
		lastErr = err
		if !isRetryableErr(err) {
			return err // fatal — fail fast
		}
	}
	return lastErr
}
