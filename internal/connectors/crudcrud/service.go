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
// before we ship it upstream. crudcrud accepts garbage and 400s on
// it — fail fast so the run row carries a useful error message.
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
