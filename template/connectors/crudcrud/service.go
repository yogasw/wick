package crudcrud

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// service.go holds pure Go logic — input validation, URL construction,
// any shape-massaging that doesn't touch the network. Keep it free of
// http calls so handlers can compose it without dragging I/O along.

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

// maskSecretWords replaces every keyword listed in the secret_words
// config (one per row) with a wick_enc_ token in the marshaled response,
// scoped to the calling user's per-user key. Returns the original
// response unchanged when secret_words is empty or marshaling fails.
//
// We round-trip through json.RawMessage so the framework's outer marshal
// preserves the structured shape instead of double-encoding the masked
// payload as a JSON string.
func maskSecretWords(c *connector.Ctx, resp any) (any, error) {
	words := splitSecretWords(c.Cfg("secret_words"))
	if len(words) == 0 || resp == nil {
		return resp, nil
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return resp, nil
	}
	var masked string
	if c.CfgBool("secret_words_ignore_case") {
		masked = c.MaskIgnoreCase(string(raw), words)
	} else {
		masked = c.Mask(string(raw), words)
	}
	return json.RawMessage(masked), nil
}

// splitSecretWords parses the kvlist JSON value into a flat list of
// keywords. The bare-kvlist storage shape is a JSON array of one-key
// objects, e.g. `[{"value":"foo"},{"value":"bar"}]`. Empty values and
// malformed input yield an empty list (passthrough behaviour).
func splitSecretWords(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var rows []map[string]string
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil
	}
	var out []string
	for _, row := range rows {
		v := strings.TrimSpace(row["value"])
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
