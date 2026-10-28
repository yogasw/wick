package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

const defaultBaseURL = "https://gitlab.com"

// apiError is a non-2xx GitLab response.
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string { return fmt.Sprintf("gitlab %d: %s", e.Status, e.Message) }

// apiURL joins the instance's /api/v4 root, path and query.
func apiURL(c *connector.Ctx, path string, q url.Values) string {
	base := strings.TrimRight(strings.TrimSpace(c.Cfg("base_url")), "/")
	if base == "" {
		base = defaultBaseURL
	}
	base = strings.TrimSuffix(base, "/api/v4")
	u := base + "/api/v4" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// doJSON sends an authenticated request and decodes the JSON answer.
func doJSON(c *connector.Ctx, method, u string, body any) (any, error) {
	raw, _, err := doRaw(c, method, u, body)
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{"ok": true}, nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return out, nil
}

// doRaw sends an authenticated request and returns the body as is (job
// logs, raw files). Errors carry GitLab's message / error field.
func doRaw(c *connector.Ctx, method, u string, body any) ([]byte, http.Header, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(c.Context(), method, u, reader)
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	token := strings.TrimSpace(c.Cfg("token"))
	if token == "" {
		return nil, nil, fmt.Errorf("token is not configured for this connector instance")
	}
	req.Header.Set("PRIVATE-TOKEN", token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("gitlab %s %s: %w", method, redactURL(u), err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.Header, &apiError{Status: resp.StatusCode, Message: errorMessage(raw)}
	}
	return raw, resp.Header, nil
}

// errorMessage pulls GitLab's message/error field ({"message": "..."},
// {"message": {"field": ["..."]}}, {"error": "..."}) out of an error body.
func errorMessage(raw []byte) string {
	var e struct {
		Message any    `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil {
		switch m := e.Message.(type) {
		case string:
			if m != "" {
				return m
			}
		case nil:
		default:
			if b, err := json.Marshal(m); err == nil {
				return string(b)
			}
		}
		if e.Error != "" {
			return e.Error
		}
	}
	return strings.TrimSpace(string(raw))
}

func redactURL(u string) string {
	if i := strings.Index(u, "?"); i >= 0 {
		return u[:i]
	}
	return u
}
