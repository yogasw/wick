package crudcrud

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// repo.go owns every outbound network call. Operation handlers compose
// it with service.go helpers but never reach for net/http themselves.
//
// doRequest issues the HTTP call, surfaces non-2xx as a typed error,
// and decodes the response as JSON when the body is non-empty. Empty
// bodies (typical for PUT/DELETE) yield a nil map.
//
// http.NewRequestWithContext is mandatory: every connector MUST plumb
// c.Context() into outbound requests so MCP cancellations (client
// disconnect, deadline) abort the upstream call instead of leaking
// the goroutine.
func doRequest(c *connector.Ctx, method, url string, body []byte) (any, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(c.Context(), method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call crudcrud: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = resp.Status
		}
		return nil, fmt.Errorf("crudcrud %d: %s", resp.StatusCode, msg)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode response: %w (body: %s)", err, truncate(string(raw), 200))
	}
	return decoded, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
