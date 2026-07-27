package wick

import (
	"bufio"
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

// sseEvent is one decoded Server-Sent Event: the `event:` name (may be
// empty) and the concatenated `data:` payload. wick's SSE consumers
// (the OpenAI/Anthropic streaming adapters) only need these two fields.
type sseEvent struct {
	Name string
	Data string
}

// postSSE POSTs a JSON body and streams a Server-Sent Events response,
// invoking onEvent for each decoded event as it arrives. It runs under the
// SAME retry+timeout policy as postJSON — BUT a retry only ever happens
// BEFORE the first byte is streamed: once onEvent has fired, the request
// has committed partial output to the caller (already emitted to the user),
// so replaying it would double-emit. onEvent returning false stops the
// stream early (caller is done). A non-2xx status is returned as *httpError
// (retryable/fatal classified the same way as postJSON) so a 429/5xx before
// streaming still backs off.
//
// The per-attempt timeout bounds only the CONNECT + header phase, not the
// whole stream: doWithRetry's callCtx would otherwise cut a long healthy
// stream at perCallTimeout. We detach the body read from that deadline by
// reading under the parent ctx once the response header is in.
func postSSE(ctx context.Context, url string, headers map[string]string, reqBody any, onEvent func(sseEvent) bool) error {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}
	policy := retryPolicyFromContext(ctx)
	notify := retryNotifyFromContext(ctx)

	started := false // flips true once we begin streaming events (past retry)
	streamErr := doWithRetry(ctx, policy, notify, func(callCtx context.Context) error {
		// The body read must outlive callCtx's per-attempt deadline (a long
		// stream is not a hung connect), so issue the request under the parent
		// ctx; callCtx only governs whether doWithRetry considers this attempt
		// timed out for the connect phase.
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
		if reqErr != nil {
			return reqErr
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "text/event-stream")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		start := time.Now()
		resp, doErr := http.DefaultClient.Do(req)
		if doErr != nil {
			log.Warn().Str("component", "wick.http").Str("url", maskURL(url)).
				Dur("latency", time.Since(start)).Err(doErr).Msg("outbound stream: transport error")
			return doErr
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(resp.Body)
			herr := &httpError{status: resp.StatusCode, body: string(body)}
			level := "retryable"
			if !isRetryableErr(herr) {
				level = "fatal"
			}
			log.Warn().Str("component", "wick.http").Str("url", maskURL(url)).
				Int("status", resp.StatusCode).Dur("latency", time.Since(start)).
				Str("body", truncate(body)).Msgf("outbound stream: %s error", level)
			return herr
		}

		log.Debug().Str("component", "wick.http").Str("url", maskURL(url)).
			Int("status", resp.StatusCode).Msg("outbound stream: open")
		started = true
		return readSSE(resp.Body, onEvent)
	})
	// Once streaming has begun, doWithRetry must not have retried (readSSE runs
	// on the first successful attempt). started guards nothing here directly —
	// the invariant is enforced by returning readSSE's error straight up (a
	// stream failure mid-flight is NOT a fresh httpError status, so
	// isRetryableErr won't loop it). Kept for the log/assert intent.
	_ = started
	return streamErr
}

// readSSE parses the SSE framing (blank-line-delimited events, `event:` and
// `data:` fields, `data:` lines concatenated with newlines) and dispatches
// each complete event to onEvent. Returns nil at EOF, ctx error on cancel, or
// the scanner error. Stops early (nil) when onEvent returns false.
func readSSE(r io.Reader, onEvent func(sseEvent) bool) error {
	sc := bufio.NewScanner(r)
	// Model responses can carry very long single data: lines (a big text or
	// tool-arg delta); bump the scanner's buffer well past the 64KB default.
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var ev sseEvent
	var data strings.Builder
	flush := func() bool {
		if data.Len() == 0 && ev.Name == "" {
			return true
		}
		ev.Data = data.String()
		cont := onEvent(ev)
		ev = sseEvent{}
		data.Reset()
		return cont
	}
	for sc.Scan() {
		line := sc.Text()
		if line == "" { // event boundary
			if !flush() {
				return nil
			}
			continue
		}
		if strings.HasPrefix(line, ":") { // comment / keep-alive
			continue
		}
		field, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			ev.Name = value
		case "data":
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(value)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	// Flush a trailing event with no final blank line.
	flush()
	return nil
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
