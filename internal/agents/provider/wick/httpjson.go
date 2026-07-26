package wick

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// The per-call timeout and retry count now live in retry.go's retryPolicy
// (defaultCallTimeout / defaultMaxAttempts), applied uniformly across every
// adapter via doWithRetry and overridable by the operator's wick config.

// retryNotify is called by postJSON just before it sleeps to retry a retryable
// failure, so a caller (the engine) can surface "retrying (attempt N — reason)"
// live. attempt is the upcoming 1-based try; reason is a short label.
type retryNotify func(attempt int, reason string)

// retryNotifyKey carries a retryNotify through the context so postJSON can call
// it without changing every adapter signature. Absent → retries stay silent.
type retryNotifyKey struct{}

// withRetryNotify returns a context that carries fn, consulted by postJSON on
// each retry. The engine attaches this around the model call so HTTP-layer
// retries (429/5xx/transport) become visible in the interactions UI.
func withRetryNotify(ctx context.Context, fn retryNotify) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, retryNotifyKey{}, fn)
}

func retryNotifyFromContext(ctx context.Context) retryNotify {
	fn, _ := ctx.Value(retryNotifyKey{}).(retryNotify)
	return fn
}

// httpError carries the vendor status + body so vendorErrorMessage can
// classify it (401/404 fatal vs 429/5xx retryable).
type httpError struct {
	status int
	body   string
}

func (e *httpError) Error() string {
	b := e.body
	if len(b) > 500 {
		b = b[:500]
	}
	return fmt.Sprintf("HTTP %d: %s", e.status, b)
}

// postJSON sends a JSON request and decodes a JSON response under the shared
// retry+timeout policy (doWithRetry): each attempt is timeout-bounded, retryable
// failures (429/5xx/transport) back off and retry, and fatal statuses (4xx
// except 429) fail fast. The policy (attempt count + timeout) and the live retry
// notifier come from the context so the operator's configured knobs and the
// "retrying (attempt N)" badge apply uniformly with the other adapters.
func postJSON(ctx context.Context, url string, headers map[string]string, reqBody any, out any) error {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	policy := retryPolicyFromContext(ctx)
	notify := retryNotifyFromContext(ctx)
	return doWithRetry(ctx, policy, notify, func(callCtx context.Context) error {
		req, reqErr := http.NewRequestWithContext(callCtx, http.MethodPost, url, bytes.NewReader(payload))
		if reqErr != nil {
			return reqErr
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		start := time.Now()
		resp, doErr := http.DefaultClient.Do(req)
		if doErr != nil {
			// Transport error — retryable (isRetryableErr matches net timeouts /
			// connection errors). Logged masked for diagnosis.
			log.Warn().Str("component", "wick.http").Str("url", maskURL(url)).
				Dur("latency", time.Since(start)).Err(doErr).Msg("outbound call: transport error")
			return doErr
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		latency := time.Since(start)

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			log.Debug().Str("component", "wick.http").Str("url", maskURL(url)).
				Int("status", resp.StatusCode).Dur("latency", latency).
				Int("req_bytes", len(payload)).Int("resp_bytes", len(body)).
				Msg("outbound call: ok")
			if err := json.Unmarshal(body, out); err != nil {
				return fmt.Errorf("decode response: %w (body: %s)", err, truncate(body))
			}
			return nil
		}
		herr := &httpError{status: resp.StatusCode, body: string(body)}
		// The httpError message carries the status code, which isRetryableErr
		// classifies (429/5xx retryable; 401/404/400 fatal) — so returning it
		// lets doWithRetry decide whether to retry or fail fast.
		level := "retryable"
		if !isRetryableErr(herr) {
			level = "fatal"
		}
		log.Warn().Str("component", "wick.http").Str("url", maskURL(url)).
			Int("status", resp.StatusCode).Dur("latency", latency).
			Str("body", truncate(body)).Msgf("outbound call: %s error", level)
		return herr
	})
}

// getJSON is postJSON's GET counterpart for model-discovery list calls.
func getJSON(ctx context.Context, url string, headers map[string]string, out any) error {
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &httpError{status: resp.StatusCode, body: string(body)}
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func truncate(b []byte) string {
	if len(b) > 300 {
		return string(b[:300]) + "..."
	}
	return string(b)
}

// maskURL redacts a secret carried in the query string (Gemini passes the
// API key as ?key=…) so it never lands in logs.
func maskURL(u string) string {
	i := strings.Index(u, "key=")
	if i < 0 {
		return u
	}
	end := strings.IndexByte(u[i:], '&')
	if end < 0 {
		return u[:i] + "key=***"
	}
	return u[:i] + "key=***" + u[i+end:]
}
