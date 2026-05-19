// Package httprest is a generic HTTP/REST connector that lets an LLM call
// any JSON API without writing a custom connector. One instance wraps a
// single API base URL and auth scheme; operations cover the five standard
// HTTP verbs (GET, POST, PUT, PATCH, DELETE).
//
// File layout:
//
//   - connector.go — Meta, Configs, per-op Input structs, Operations, thin handlers
//   - service.go   — URL assembly, header building, query-string parsing
//   - repo.go      — outbound HTTP I/O via http.NewRequestWithContext
package httprest

import (
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// Key is the connector definition slug used in the registry and in
// MCP tool IDs (conn:{instance_id}/get, conn:{instance_id}/post, …).
const Key = "httprest"

// Configs is the per-instance credential and endpoint set. Every field is
// reflected by entity.StructToConfigs into the admin form and MCP schema.
type Configs struct {
	BaseURL     string `wick:"url;required;desc=Base URL of the target API. Example: https://api.example.com/v1"`
	AuthHeader  string `wick:"desc=Header name used for authentication. Example: Authorization or X-API-Key. Leave empty to skip auth."`
	AuthValue   string `wick:"secret;desc=Value for the auth header. Example: Bearer mytoken or myapikey123"`
	TimeoutSecs int    `wick:"desc=Per-request timeout in seconds. Default: 30"`
}

// GetInput is the argument schema for the GET operation.
type GetInput struct {
	Path  string `wick:"required;desc=Path relative to BaseURL. Example: /users/42 or /repos/owner/name"`
	Query string `wick:"desc=Query string or JSON object. Example: page=1&limit=10 or {\"state\":\"open\"}"`
}

// PostInput is the argument schema for the POST operation.
type PostInput struct {
	Path        string `wick:"required;desc=Path relative to BaseURL. Example: /users"`
	Body        string `wick:"textarea;desc=Request body. JSON is sent as application/json; plain text as text/plain."`
	ContentType string `wick:"desc=Content-Type header override. Default: application/json"`
}

// PutInput is the argument schema for the PUT operation.
type PutInput struct {
	Path        string `wick:"required;desc=Path relative to BaseURL. Example: /users/42"`
	Body        string `wick:"textarea;desc=Replacement body as JSON."`
	ContentType string `wick:"desc=Content-Type header override. Default: application/json"`
}

// PatchInput is the argument schema for the PATCH operation.
type PatchInput struct {
	Path        string `wick:"required;desc=Path relative to BaseURL. Example: /users/42"`
	Body        string `wick:"textarea;desc=Partial update body as JSON."`
	ContentType string `wick:"desc=Content-Type header override. Default: application/json"`
}

// DeleteInput is the argument schema for the DELETE operation.
type DeleteInput struct {
	Path string `wick:"required;desc=Path relative to BaseURL. Example: /users/42"`
}

// Meta returns the static metadata block for this connector definition.
func Meta() connector.Meta {
	return connector.Meta{
		Key:         Key,
		Name:        "HTTP / REST",
		Description: "Call any JSON REST API. Configure the base URL and auth once; GET, POST, PUT, PATCH, or DELETE any path at runtime.",
		Icon:        "🌐",
	}
}

// Operations returns the five LLM-callable HTTP verbs exposed by this
// connector. GET is read-only; POST/PUT/PATCH/DELETE are destructive.
func Operations() []connector.Operation {
	return []connector.Operation{
		connector.Op(
			"get",
			"GET Request",
			"Send an HTTP GET request to {base_url}/{path}. Optionally append query parameters. Returns the parsed JSON response.",
			GetInput{},
			getOp, wickdocs.Docs{},
		),
		connector.OpDestructive(
			"post",
			"POST Request",
			"Send an HTTP POST request with a JSON body to {base_url}/{path}. Returns the parsed JSON response.",
			PostInput{},
			postOp, wickdocs.Docs{},
		),
		connector.OpDestructive(
			"put",
			"PUT Request",
			"Send an HTTP PUT request (full replacement) with a JSON body to {base_url}/{path}. Returns the parsed JSON response.",
			PutInput{},
			putOp, wickdocs.Docs{},
		),
		connector.OpDestructive(
			"patch",
			"PATCH Request",
			"Send an HTTP PATCH request (partial update) with a JSON body to {base_url}/{path}. Returns the parsed JSON response.",
			PatchInput{},
			patchOp, wickdocs.Docs{},
		),
		connector.OpDestructive(
			"delete",
			"DELETE Request",
			"Send an HTTP DELETE request to {base_url}/{path}. Returns status confirmation.",
			DeleteInput{},
			deleteOp, wickdocs.Docs{},
		),
	}
}

// ── Operation handlers ───────────────────────────────────────────────
//
// Handlers are deliberately thin: build the request via service.go,
// execute it via repo.go, return the result or error.

func getOp(c *connector.Ctx) (any, error) {
	url, err := buildURL(c, c.Input("path"), c.Input("query"))
	if err != nil {
		return nil, err
	}
	return doRequest(c, "GET", url, "", "")
}

func postOp(c *connector.Ctx) (any, error) {
	url, err := buildURL(c, c.Input("path"), "")
	if err != nil {
		return nil, err
	}
	return doRequest(c, "POST", url, c.Input("body"), c.Input("content_type"))
}

func putOp(c *connector.Ctx) (any, error) {
	url, err := buildURL(c, c.Input("path"), "")
	if err != nil {
		return nil, err
	}
	return doRequest(c, "PUT", url, c.Input("body"), c.Input("content_type"))
}

func patchOp(c *connector.Ctx) (any, error) {
	url, err := buildURL(c, c.Input("path"), "")
	if err != nil {
		return nil, err
	}
	return doRequest(c, "PATCH", url, c.Input("body"), c.Input("content_type"))
}

func deleteOp(c *connector.Ctx) (any, error) {
	url, err := buildURL(c, c.Input("path"), "")
	if err != nil {
		return nil, err
	}
	resp, err := doRequest(c, "DELETE", url, "", "")
	if err != nil {
		return nil, err
	}
	if resp == nil {
		return map[string]any{"ok": true, "path": c.Input("path")}, nil
	}
	return resp, nil
}
