package slack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// doRequest sends an authenticated Slack Web API request. Slack always
// returns HTTP 200 even for errors — the actual success/failure is in
// the response body's `ok` boolean. This function surfaces Slack errors
// as Go errors so the MCP layer marks the call as isError=true.
func doRequest(c *connector.Ctx, method, url string, body any) (any, error) {
	token := strings.TrimSpace(c.Cfg("bot_token"))
	if token == "" {
		return nil, fmt.Errorf("bot_token is not configured for this connector instance")
	}

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

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("slack %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, fmt.Errorf("decode slack response: %w", err)
	}

	// Slack signals errors in the body, not HTTP status.
	if ok, _ := decoded["ok"].(bool); !ok {
		errCode, _ := decoded["error"].(string)
		if errCode == "" {
			errCode = "unknown_error"
		}
		return nil, fmt.Errorf("slack api error: %s", errCode)
	}

	return decoded, nil
}
