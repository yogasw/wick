package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// Config is the connector's settings. Admins fill these per instance from the
// app UI; the host stores them (secrets encrypted) and injects them into each
// Ctx. The `wick:"..."` tags name the fields and mark secrets.
// The field NAME becomes the config key in snake_case (BaseURL → base_url), so
// you rarely set key= explicitly. Inside one `wick:"..."` tag, entries are
// `;`-separated: `desc=...` is the help text, `secret` marks an encrypted field,
// `required` makes it mandatory.
type Config struct {
	BaseURL string `wick:"required;url;desc=API base URL. Example: https://api.example.com"`
	Token   string `wick:"secret;desc=Bearer token sent as the Authorization header."`
}

// getInput / deleteInput are the per-operation argument structs. One struct per
// op keeps each operation's schema explicit and self-documenting.
type getInput struct {
	Path string `wick:"required;desc=Path appended to base_url. Example: /things/123"`
}

type deleteInput struct {
	Path string `wick:"required;desc=Path of the resource to delete."`
}

// Module returns the connector definition. Rename Meta.Key/Name and replace the
// operations with your own — this is the only file you normally edit.
func Module() connector.Module {
	return connector.Module{
		Meta: connector.Meta{
			// Key MUST equal your folder name (lowercase a-z/0-9/_ only, no '-').
			// It's the slug used for the zip, install dir, and registry — the
			// build fails if Key != folder. e.g. folder connector/gmail/ → Key "gmail".
			Key:         "template",
			Name:        "Template Connector", // free display name (spaces/caps OK)
			Description: "Starter connector: GET and DELETE against a configurable HTTP API.",
			Icon:        "🔌",
		},
		Configs: entity.StructToConfigs(Config{}),
		Operations: []connector.Category{
			connector.Cat("Resources", "Read and delete resources",
				connector.Op("get", "Get Resource",
					"GET base_url + path and return the parsed JSON (or raw body).",
					getInput{}, doGet, wickdocs.Docs{}),
				connector.OpDestructive("delete", "Delete Resource",
					"DELETE base_url + path. Marked destructive — the agent must opt in.",
					deleteInput{}, doDelete, wickdocs.Docs{}),
			),
		},
	}
}

// doGet performs the GET operation. Always build the request with
// http.NewRequestWithContext(c.Context(), ...) so the call is cancelled when the
// host cancels the operation (prevents goroutine leaks).
func doGet(c *connector.Ctx) (any, error) {
	url := strings.TrimRight(c.Cfg("base_url"), "/") + "/" + strings.TrimLeft(c.Input("path"), "/")
	req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if tok := c.Cfg("token"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("GET %s: status %d: %s", url, resp.StatusCode, truncate(string(body), 300))
	}
	// Return parsed JSON when possible so the agent gets structured data; fall
	// back to the raw string otherwise.
	var parsed any
	if json.Unmarshal(body, &parsed) == nil {
		return parsed, nil
	}
	return string(body), nil
}

// doDelete performs the DELETE operation.
func doDelete(c *connector.Ctx) (any, error) {
	url := strings.TrimRight(c.Cfg("base_url"), "/") + "/" + strings.TrimLeft(c.Input("path"), "/")
	req, err := http.NewRequestWithContext(c.Context(), http.MethodDelete, url, nil)
	if err != nil {
		return nil, err
	}
	if tok := c.Cfg("token"); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("DELETE %s: status %d: %s", url, resp.StatusCode, truncate(string(body), 300))
	}
	return map[string]any{"deleted": true, "status": resp.StatusCode}, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
