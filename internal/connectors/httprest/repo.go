package httprest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/yogasw/wick/pkg/connector"
)

// repo.go owns every outbound HTTP call. All operations funnel through
// doRequest — handlers never import net/http directly.
//
// http.NewRequestWithContext is mandatory: every request MUST carry
// c.Context() so MCP cancellations and per-call deadlines abort the
// upstream call instead of leaking the goroutine.

// doRequest sends an HTTP request and decodes the response as JSON.
// method is the HTTP verb (GET, POST, PUT, PATCH, DELETE).
// url is the fully assembled target URL (base + path + query).
// rawBody is the string body supplied by the LLM; empty means no body.
// contentType is used when rawBody is non-empty; defaults to application/json.
func doRequest(c *connector.Ctx, method, url, rawBody, contentType string) (any, error) {
	body, err := prepareBody(rawBody)
	if err != nil {
		return nil, err
	}

	// Wrap c.HTTP in a timeout-scoped client when a custom timeout is set.
	// We create a shallow copy so we don't mutate the shared client.
	client := clientWithTimeout(c)

	req, err := http.NewRequestWithContext(c.Context(), method, url, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Accept", "application/json")

	if body != nil {
		req.Header.Set("Content-Type", resolveContentType(contentType))
	}

	if key, val := authHeaders(c); key != "" && val != "" {
		req.Header.Set(key, val)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4 MB cap

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("%s %s → %d: %s", method, url, resp.StatusCode, truncate(msg, 300))
	}

	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, nil
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		// Non-JSON response — return as plain text so the LLM can still read it.
		return map[string]any{
			"status": resp.StatusCode,
			"body":   truncate(string(raw), 2000),
		}, nil
	}
	return decoded, nil
}

// prepareBody converts the LLM-supplied string body into an io.Reader.
// Returns nil for empty bodies so requests without a body are sent correctly.
func prepareBody(raw string) (io.Reader, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	return bytes.NewBufferString(raw), nil
}

// clientWithTimeout returns a *http.Client scoped to the configured timeout.
// It reuses c.HTTP's transport to preserve connection pooling; only the
// Timeout field is overridden.
func clientWithTimeout(c *connector.Ctx) *http.Client {
	secs := timeoutSecs(c)
	return &http.Client{
		Transport: c.HTTP.Transport,
		Timeout:   time.Duration(secs) * time.Second,
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
