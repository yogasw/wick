package httprest

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// service.go holds pure Go logic — URL assembly, header construction,
// query-string parsing. No network calls here so handlers can unit-test
// it without spinning up an HTTP server.

// buildURL joins the configured BaseURL with the caller-supplied path and
// optional query string. The query argument may be either a URL-encoded
// string ("page=1&limit=10") or a JSON object ("{\"page\":1}") — both
// forms are normalised to a url.Values map before appending.
func buildURL(c *connector.Ctx, path, query string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(c.Cfg("base_url")), "/")
	if base == "" {
		return "", errors.New("base_url is not configured for this connector instance")
	}

	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("path is required")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	full := base + path

	query = strings.TrimSpace(query)
	if query == "" {
		return full, nil
	}

	qs, err := parseQuery(query)
	if err != nil {
		return "", fmt.Errorf("invalid query: %w", err)
	}
	if len(qs) > 0 {
		full += "?" + qs.Encode()
	}
	return full, nil
}

// parseQuery accepts either a URL-encoded string ("page=1&limit=10") or a
// flat JSON object ("{\"page\":1,\"q\":\"hello\"}") and returns url.Values.
// Nested JSON objects are not supported — values are stringified via fmt.Sprint.
func parseQuery(raw string) (url.Values, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}

	// JSON object path
	if strings.HasPrefix(raw, "{") {
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return nil, fmt.Errorf("query is not valid JSON object: %w", err)
		}
		vals := make(url.Values, len(m))
		for k, v := range m {
			vals.Set(k, fmt.Sprint(v))
		}
		return vals, nil
	}

	// URL-encoded string path
	vals, err := url.ParseQuery(raw)
	if err != nil {
		return nil, fmt.Errorf("query is not valid URL-encoded string: %w", err)
	}
	return vals, nil
}

// authHeaders returns the auth header key/value pair configured for this
// instance. Returns empty strings when auth is not configured.
func authHeaders(c *connector.Ctx) (key, value string) {
	key = strings.TrimSpace(c.Cfg("auth_header"))
	value = strings.TrimSpace(c.Cfg("auth_value"))
	return key, value
}

// resolveContentType returns the effective Content-Type to use for a request
// body. Falls back to "application/json" when the caller did not specify one
// and the body is non-empty.
func resolveContentType(override string) string {
	ct := strings.TrimSpace(override)
	if ct == "" {
		return "application/json"
	}
	return ct
}

// timeoutSecs returns the configured per-request timeout, defaulting to 30
// when the field is zero or absent.
func timeoutSecs(c *connector.Ctx) int {
	t := c.CfgInt("timeout_secs")
	if t <= 0 {
		return 30
	}
	return t
}
