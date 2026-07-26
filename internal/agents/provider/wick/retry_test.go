package wick

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestIsRetryableErr(t *testing.T) {
	retryable := []error{
		context.DeadlineExceeded,
		errors.New("HTTP 429: rate limit exceeded"),
		errors.New("HTTP 503: service unavailable"),
		errors.New("HTTP 500: internal error"),
		errors.New("dial tcp: connection refused"),
		errors.New("read: connection reset by peer"),
		errors.New("model is overloaded, please try again"),
		errors.New("context deadline exceeded"),
	}
	for _, e := range retryable {
		if !isRetryableErr(e) {
			t.Errorf("expected retryable: %v", e)
		}
	}
	fatal := []error{
		errors.New("HTTP 401: invalid_api_key"),
		errors.New("HTTP 404: model_not_found"),
		errors.New("HTTP 400: bad request"),
		context.Canceled,
		nil,
	}
	for _, e := range fatal {
		if isRetryableErr(e) {
			t.Errorf("expected NOT retryable: %v", e)
		}
	}
	// A 429 that also mentions a fatal token must stay fatal (fatal wins).
	if isRetryableErr(errors.New("401 unauthorized (rate limit note)")) {
		t.Error("fatal signal should win over retryable token")
	}
}

func TestDoWithRetrySucceedsAfterTransient(t *testing.T) {
	calls := 0
	var notified []int
	notify := func(attempt int, reason string) { notified = append(notified, attempt) }
	policy := retryPolicy{maxAttempts: 3, baseBackoff: time.Millisecond}

	err := doWithRetry(context.Background(), policy, notify, func(callCtx context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("HTTP 503: unavailable")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if calls != 3 {
		t.Fatalf("expected 3 calls, got %d", calls)
	}
	// notify fired before attempt 2 and 3.
	if len(notified) != 2 || notified[0] != 2 || notified[1] != 3 {
		t.Fatalf("notify attempts = %v, want [2 3]", notified)
	}
}

func TestDoWithRetryFatalFailsFast(t *testing.T) {
	calls := 0
	policy := retryPolicy{maxAttempts: 3, baseBackoff: time.Millisecond}
	err := doWithRetry(context.Background(), policy, nil, func(callCtx context.Context) error {
		calls++
		return errors.New("HTTP 401: invalid_api_key")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("fatal error should not retry; calls = %d", calls)
	}
}

func TestDoWithRetryExhausts(t *testing.T) {
	calls := 0
	policy := retryPolicy{maxAttempts: 3, baseBackoff: time.Millisecond}
	err := doWithRetry(context.Background(), policy, nil, func(callCtx context.Context) error {
		calls++
		return errors.New("HTTP 503: unavailable")
	})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if calls != 3 {
		t.Fatalf("expected 3 attempts, got %d", calls)
	}
}

func TestDoWithRetryPerAttemptTimeout(t *testing.T) {
	policy := retryPolicy{maxAttempts: 1, callTimeout: 20 * time.Millisecond}
	err := doWithRetry(context.Background(), policy, nil, func(callCtx context.Context) error {
		<-callCtx.Done() // simulate a hung call
		return callCtx.Err()
	})
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestRetryPolicyFromConfigDefaults(t *testing.T) {
	// 0/0 → defaults.
	p := retryPolicyFromConfig(0, 0)
	if p.attempts() != defaultMaxAttempts || p.timeout() != defaultCallTimeout {
		t.Fatalf("defaults not applied: attempts=%d timeout=%v", p.attempts(), p.timeout())
	}
	// explicit overrides honored.
	p = retryPolicyFromConfig(5, 30)
	if p.attempts() != 5 || p.timeout() != 30*time.Second {
		t.Fatalf("overrides not applied: attempts=%d timeout=%v", p.attempts(), p.timeout())
	}
}
