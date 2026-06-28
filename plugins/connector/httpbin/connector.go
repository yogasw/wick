package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/wickdocs"
	"github.com/yogasw/wick/plugins/tags"
)

// Config is httpbin's per-instance settings. base_url defaults to the public
// service so the connector works with zero setup; point it at a self-hosted
// httpbin if you have one.
type Config struct {
	BaseURL string `wick:"url;default=https://httpbin.org;desc=httpbin base URL. Default: https://httpbin.org"`
}

type getInput struct {
	Query string `wick:"desc=Optional query string echoed back, e.g. foo=bar&x=1"`
}

type postInput struct {
	Body string `wick:"textarea;required;desc=Raw body to POST. JSON is echoed back parsed under \"json\"."`
}

type statusInput struct {
	Code string `wick:"required;desc=HTTP status code httpbin should return, e.g. 200, 404, 503"`
}

// Module is the connector definition: three operations against httpbin that
// together exercise GET (with query), POST (with body), and arbitrary status
// codes — enough to validate the whole plugin path.
func Module() connector.Module {
	return connector.Module{
		Meta: connector.Meta{
			Key:         "httpbin",
			Name:        "HTTPBin",
			Description: "Sample connector hitting httpbin.org — GET, POST, and status-code echo. No credentials needed.",
			Icon:        "🧪",
			// DefaultTags work like a built-in connector's, from the shared
			// plugins/tags catalog: Connector drops it into the connector group,
			// API files it under that section. The app reads these from the
			// manifest and categorizes the plugin identically to a built-in.
			DefaultTags: []entity.DefaultTag{tags.Connector, tags.API},
		},
		Configs: entity.StructToConfigs(Config{}),
		Operations: []connector.Category{
			connector.Cat("Requests", "Send HTTP requests to httpbin",
				connector.Op("get", "GET /get",
					"GET {base_url}/get?{query} and return httpbin's echo of the request.",
					getInput{}, doGet, wickdocs.Docs{}),
				connector.Op("post", "POST /post",
					"POST a body to {base_url}/post and return httpbin's echo.",
					postInput{}, doPost, wickdocs.Docs{}),
				connector.Op("status", "GET /status/{code}",
					"Ask httpbin to return a specific HTTP status code.",
					statusInput{}, doStatus, wickdocs.Docs{}),
			),
		},
	}
}

func baseURL(c *connector.Ctx) string {
	b := strings.TrimRight(c.Cfg("base_url"), "/")
	if b == "" {
		b = "https://httpbin.org"
	}
	return b
}

func doGet(c *connector.Ctx) (any, error) {
	u := baseURL(c) + "/get"
	if q := strings.TrimSpace(c.Input("query")); q != "" {
		u += "?" + q
	}
	return doJSON(c, http.MethodGet, u, nil)
}

func doPost(c *connector.Ctx) (any, error) {
	body := strings.NewReader(c.Input("body"))
	return doJSON(c, http.MethodPost, baseURL(c)+"/post", body)
}

func doStatus(c *connector.Ctx) (any, error) {
	code := strings.TrimSpace(c.Input("code"))
	if code == "" {
		return nil, fmt.Errorf("code is required")
	}
	u := baseURL(c) + "/status/" + url.PathEscape(code)
	req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return map[string]any{"requested": code, "status": resp.StatusCode}, nil
}

// doJSON runs the request (always with c.Context() so it's cancellable) and
// returns parsed JSON when the response is JSON, else the raw string.
func doJSON(c *connector.Ctx, method, u string, body io.Reader) (any, error) {
	req, err := http.NewRequestWithContext(c.Context(), method, u, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s %s: status %d", method, u, resp.StatusCode)
	}
	var parsed any
	if json.Unmarshal(raw, &parsed) == nil {
		return parsed, nil
	}
	return string(raw), nil
}
