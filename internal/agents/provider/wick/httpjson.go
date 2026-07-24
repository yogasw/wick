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

// perCallTimeout bounds one model HTTP call (harness hardening #3).
const perCallTimeout = 120 * time.Second

// maxRetries is the attempt count for retryable failures (429/5xx/
// transport) with exponential backoff (harness hardening #2).
const maxRetries = 3

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

// postJSON sends a JSON request and decodes a JSON response, retrying
// retryable failures with exponential backoff and bounding each attempt
// with perCallTimeout. Fatal statuses (4xx except 429) return
// immediately so a bad key/model doesn't burn retries.
func postJSON(ctx context.Context, url string, headers map[string]string, reqBody any, out any) error {
	payload, err := json.Marshal(reqBody)
	if err != nil {
		return fmt.Errorf("marshal request: %w", err)
	}

	var lastErr error
	backoff := 500 * time.Millisecond
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-time.After(backoff):
				backoff *= 2
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		callCtx, cancel := context.WithTimeout(ctx, perCallTimeout)
		req, reqErr := http.NewRequestWithContext(callCtx, http.MethodPost, url, bytes.NewReader(payload))
		if reqErr != nil {
			cancel()
			return reqErr
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		start := time.Now()
		resp, doErr := http.DefaultClient.Do(req)
		if doErr != nil {
			cancel()
			lastErr = doErr
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// Every outbound interaction is logged so a failing call is
			// diagnosable ("kenapa error"). Secrets in the URL (Gemini's
			// ?key=) and auth headers are masked.
			log.Warn().Str("component", "wick.http").Str("url", maskURL(url)).
				Int("attempt", attempt).Dur("latency", time.Since(start)).
				Err(doErr).Msg("outbound call: transport error, retrying")
			continue // transport errors are retryable
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cancel()
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
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = herr
			log.Warn().Str("component", "wick.http").Str("url", maskURL(url)).
				Int("status", resp.StatusCode).Int("attempt", attempt).Dur("latency", latency).
				Str("body", truncate(body)).Msg("outbound call: retryable error")
			continue
		}
		// Fatal (401/404/400) — no retry. Log the body so a bad key / model
		// / request is immediately visible in the logs.
		log.Warn().Str("component", "wick.http").Str("url", maskURL(url)).
			Int("status", resp.StatusCode).Dur("latency", latency).
			Str("body", truncate(body)).Msg("outbound call: fatal error")
		return herr
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("request failed after %d attempts", maxRetries)
	}
	return lastErr
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
