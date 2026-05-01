// Package crudcrud is the example connector shipped with the template.
// It wraps the crudcrud.com REST sandbox — a free, throwaway JSON store
// useful for demos and integration smoke tests. One row in crudcrud is
// a JSON document under an arbitrary {resource} collection; documents
// are addressed by an auto-generated _id.
//
// The single per-instance config is BaseURL — the unique endpoint URL
// crudcrud hands out when an admin claims a sandbox. Example:
//
//	https://crudcrud.com/api/<unique-id>
//
// Every operation appends "/<resource>" (and, when relevant, "/<id>")
// to BaseURL. The connector is intentionally generic: collection names
// and document shapes are LLM-supplied so a single instance can model
// any REST-ish resource the caller invents.
//
// Use this package as the canonical reference when building your own
// connector. See <https://yogasw.github.io/wick/guide/connector-module>
// for the full guide.
package crudcrud

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// Key is the connector definition slug. It is the value an admin sees
// at /manager/connectors/{Key} and the prefix of every connector_runs
// row created by this module.
const Key = "crudcrud"

// Configs is the per-instance credential / endpoint set. Reflected by
// entity.StructToConfigs into the admin form on /manager/connectors.
type Configs struct {
	BaseURL string `wick:"url;required;desc=Unique crudcrud endpoint URL. Example: https://crudcrud.com/api/abcdef0123456789"`
}

// CreateInput is the argument schema for the "create" operation.
type CreateInput struct {
	Resource string `wick:"required;desc=REST collection name. Example: books, unicorns"`
	Body     string `wick:"textarea;required;desc=JSON object to create. Example: {\"title\":\"Dune\",\"author\":\"Herbert\"}"`
}

// ListInput is the argument schema for the "list" operation.
type ListInput struct {
	Resource string `wick:"required;desc=REST collection name to list."`
}

// GetInput is the argument schema for the "get" operation.
type GetInput struct {
	Resource string `wick:"required;desc=REST collection name."`
	ID       string `wick:"required;desc=Document _id returned by create or list."`
}

// UpdateInput is the argument schema for the "update" operation.
type UpdateInput struct {
	Resource string `wick:"required;desc=REST collection name."`
	ID       string `wick:"required;desc=Document _id to overwrite."`
	Body     string `wick:"textarea;required;desc=Replacement JSON object. crudcrud does full replacement, not patch."`
}

// DeleteInput is the argument schema for the "delete" operation.
type DeleteInput struct {
	Resource string `wick:"required;desc=REST collection name."`
	ID       string `wick:"required;desc=Document _id to delete."`
}

// Meta returns the static metadata block downstream registers via
// app.RegisterConnector.
func Meta() connector.Meta {
	return connector.Meta{
		Key:         Key,
		Name:        "CRUD CRUD",
		Description: "Generic CRUD against a crudcrud.com sandbox endpoint. Useful for demos and smoke tests.",
		Icon:        "🧪",
	}
}

// Operations returns the LLM-callable actions exposed by this connector.
// Order is stable so MCP wick_list output reads predictably (create,
// list, get, update, delete).
func Operations() []connector.Operation {
	return []connector.Operation{
		connector.Op(
			"create",
			"Create Document",
			"Create a new JSON document under {resource}. crudcrud auto-generates an _id and returns the stored document.",
			CreateInput{},
			create,
		),
		connector.Op(
			"list",
			"List Documents",
			"List every document in {resource}. Returns an array; empty when the collection has no entries yet.",
			ListInput{},
			list,
		),
		connector.Op(
			"get",
			"Get Document",
			"Fetch a single document from {resource} by its _id.",
			GetInput{},
			get,
		),
		connector.Op(
			"update",
			"Update Document",
			"Replace the document at {resource}/{id} with the provided JSON. Full replacement, not a partial patch.",
			UpdateInput{},
			update,
		),
		connector.OpDestructive(
			"delete",
			"Delete Document",
			"Permanently delete the document at {resource}/{id}. Cannot be undone.",
			DeleteInput{},
			deleteOp,
		),
	}
}

// ── Operation handlers ───────────────────────────────────────────────

func create(c *connector.Ctx) (any, error) {
	resource, err := requireResource(c)
	if err != nil {
		return nil, err
	}
	body, err := requireJSONBody(c.Input("body"))
	if err != nil {
		return nil, err
	}
	url, err := buildURL(c, resource, "")
	if err != nil {
		return nil, err
	}
	return doRequest(c, http.MethodPost, url, body)
}

func list(c *connector.Ctx) (any, error) {
	resource, err := requireResource(c)
	if err != nil {
		return nil, err
	}
	url, err := buildURL(c, resource, "")
	if err != nil {
		return nil, err
	}
	return doRequest(c, http.MethodGet, url, nil)
}

func get(c *connector.Ctx) (any, error) {
	resource, id, err := requireResourceAndID(c)
	if err != nil {
		return nil, err
	}
	url, err := buildURL(c, resource, id)
	if err != nil {
		return nil, err
	}
	return doRequest(c, http.MethodGet, url, nil)
}

func update(c *connector.Ctx) (any, error) {
	resource, id, err := requireResourceAndID(c)
	if err != nil {
		return nil, err
	}
	body, err := requireJSONBody(c.Input("body"))
	if err != nil {
		return nil, err
	}
	url, err := buildURL(c, resource, id)
	if err != nil {
		return nil, err
	}
	if _, err := doRequest(c, http.MethodPut, url, body); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": id, "resource": resource}, nil
}

func deleteOp(c *connector.Ctx) (any, error) {
	resource, id, err := requireResourceAndID(c)
	if err != nil {
		return nil, err
	}
	url, err := buildURL(c, resource, id)
	if err != nil {
		return nil, err
	}
	if _, err := doRequest(c, http.MethodDelete, url, nil); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "id": id, "resource": resource}, nil
}

// ── Helpers ──────────────────────────────────────────────────────────

func requireResource(c *connector.Ctx) (string, error) {
	r := strings.TrimSpace(c.Input("resource"))
	if r == "" {
		return "", errors.New("resource is required")
	}
	return r, nil
}

func requireResourceAndID(c *connector.Ctx) (string, string, error) {
	r, err := requireResource(c)
	if err != nil {
		return "", "", err
	}
	id := strings.TrimSpace(c.Input("id"))
	if id == "" {
		return "", "", errors.New("id is required")
	}
	return r, id, nil
}

// requireJSONBody validates that the LLM-supplied body parses as JSON
// before we ship it upstream. crudcrud accepts garbage and 400s on it
// — fail fast so the run row carries a useful error message.
func requireJSONBody(raw string) ([]byte, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("body is required")
	}
	var probe any
	if err := json.Unmarshal([]byte(raw), &probe); err != nil {
		return nil, fmt.Errorf("body is not valid JSON: %w", err)
	}
	return []byte(raw), nil
}

func buildURL(c *connector.Ctx, resource, id string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(c.Cfg("base_url")), "/")
	if base == "" {
		return "", errors.New("base_url is not configured for this connector")
	}
	if id != "" {
		return base + "/" + resource + "/" + id, nil
	}
	return base + "/" + resource, nil
}

// doRequest issues the HTTP call, surfaces non-2xx as a typed error,
// and decodes the response as JSON when the body is non-empty. Empty
// bodies (typical for PUT/DELETE) yield a nil map.
//
// Note the use of http.NewRequestWithContext: every connector MUST
// propagate c.Context() so the call aborts when the MCP transport
// cancels (client disconnect, deadline) instead of leaking a goroutine
// that waits on the upstream response.
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
