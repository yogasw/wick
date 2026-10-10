package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// apiError is a non-2xx GitHub response. Error() keeps the historical
// "github <status>: <message>" text; Status lets callers react to a
// specific code (the settings ops explain 403/404 as a permission gap).
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string { return fmt.Sprintf("github %d: %s", e.Status, e.Message) }

// doRequest sends an authenticated GitHub API request and decodes the
// JSON response. body is JSON-marshaled when non-nil (POST/PATCH).
// Every request carries the token from Configs and sets the Accept header
// for GitHub API v3 plus fine-grained token compatibility.
func doRequest(c *connector.Ctx, method, url string, body any) (any, error) {
	decoded, _, err := doRequestHeaders(c, method, url, body)
	return decoded, err
}

// doRequestHeaders is doRequest that also returns the response headers,
// which carry the token's scopes (X-OAuth-Scopes) and expiry. Headers are
// returned on API errors too; nil only when no response arrived.
func doRequestHeaders(c *connector.Ctx, method, url string, body any) (any, http.Header, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, nil, fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(c.Context(), method, url, reader)
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}

	token := strings.TrimSpace(c.Cfg("token"))
	if token == "" {
		return nil, nil, fmt.Errorf("token is not configured for this connector instance")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("github %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Surface GitHub's error message directly — it's human-readable.
		var ghErr struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(raw, &ghErr); err == nil && ghErr.Message != "" {
			return nil, resp.Header, &apiError{Status: resp.StatusCode, Message: ghErr.Message}
		}
		return nil, resp.Header, &apiError{Status: resp.StatusCode, Message: strings.TrimSpace(string(raw))}
	}

	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, resp.Header, nil
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, resp.Header, fmt.Errorf("decode response: %w", err)
	}
	return decoded, resp.Header, nil
}

// doRequestText sends an authenticated GitHub API request and returns the
// raw response body as a string, without JSON-decoding it. It mirrors
// doRequest's auth headers but sets Accept to the caller-supplied value —
// useful for media-type endpoints like the PR ".diff" / ".patch" views.
// Non-2xx responses still surface GitHub's {"message"} when the error body
// is JSON, falling back to the raw text otherwise.
func doRequestText(c *connector.Ctx, method, url, accept string, body any) (string, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return "", fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(c.Context(), method, url, reader)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}

	token := strings.TrimSpace(c.Cfg("token"))
	if token == "" {
		return "", fmt.Errorf("token is not configured for this connector instance")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("github %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Surface GitHub's error message directly — it's human-readable.
		var ghErr struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(raw, &ghErr); err == nil && ghErr.Message != "" {
			return "", fmt.Errorf("github %d: %s", resp.StatusCode, ghErr.Message)
		}
		return "", fmt.Errorf("github %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	return string(raw), nil
}
