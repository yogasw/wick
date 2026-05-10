package github

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// doRequest sends an authenticated GitHub API request and decodes the
// JSON response. body is JSON-marshaled when non-nil (POST/PATCH).
// Every request carries the token from Configs and sets the Accept header
// for GitHub API v3 plus fine-grained token compatibility.
func doRequest(c *connector.Ctx, method, url string, body any) (any, error) {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(c.Context(), method, url, reader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	token := strings.TrimSpace(c.Cfg("token"))
	if token == "" {
		return nil, fmt.Errorf("token is not configured for this connector instance")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Surface GitHub's error message directly — it's human-readable.
		var ghErr struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(raw, &ghErr); err == nil && ghErr.Message != "" {
			return nil, fmt.Errorf("github %d: %s", resp.StatusCode, ghErr.Message)
		}
		return nil, fmt.Errorf("github %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}

	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return decoded, nil
}
