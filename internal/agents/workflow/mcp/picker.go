// Package mcp — workflow_picker_resolve implementation.
//
// Picker fields (channel_id whitelist, user whitelist, etc.) accept
// only `[{id, name}, ...]` JSON. AI authoring a match filter has to
// resolve the actual IDs from text the user typed ("#support",
// "yoga@example.com"). Without help it tends to guess C123 / U456
// shapes — which always fail.
//
// workflow_picker_resolve maps a `source` name (matching the
// wick:"picker=<source>" tag on the field) to a list of `{id, name}`
// items. The pickers themselves live in setup-injected functions so
// this package stays free of channel/connector imports.
package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// PickerItem is one row a picker source returns. id is what the router
// matches against; name is shown to humans.
type PickerItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// PickerFunc is the per-source resolver signature. query is an
// optional case-insensitive substring filter the caller passes — the
// implementation MAY ignore it and return everything if filtering is
// cheap downstream.
type PickerFunc func(ctx context.Context, query string) ([]PickerItem, error)

// PickerRegistry holds the wired (source → resolver) map. Concurrent-
// safe — registration happens at setup; reads happen per MCP call.
//
// Wiring is intentionally external: setup code that owns the slack
// channel / connector instances calls Register("slack.channels", fn)
// rather than mcp importing slack. Keeps the mcp package free of
// channel-specific imports.
type PickerRegistry struct {
	mu      sync.RWMutex
	sources map[string]PickerFunc
}

// NewPickerRegistry constructs an empty registry. The Ops struct
// auto-creates one in New().
func NewPickerRegistry() *PickerRegistry {
	return &PickerRegistry{sources: map[string]PickerFunc{}}
}

// Register adds (or replaces) one source. Idempotent — setup may call
// this multiple times during hot-reload.
func (r *PickerRegistry) Register(source string, fn PickerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sources[source] = fn
}

// Get returns the resolver for source, or (nil, false).
func (r *PickerRegistry) Get(source string) (PickerFunc, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	fn, ok := r.sources[source]
	return fn, ok
}

// Sources lists the registered source names, sorted. Used in error
// messages so AI sees what IS available when it asks for something
// that's not.
func (r *PickerRegistry) Sources() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.sources))
	for k := range r.sources {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// PickerResolveInput is the workflow_picker_resolve request.
type PickerResolveInput struct {
	Source string `json:"source"`
	Query  string `json:"query,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// PickerResolveResult is the response.
type PickerResolveResult struct {
	Items []PickerItem `json:"items"`
	// Truncated reports whether the resolver returned more items than
	// the caller asked for (after Limit was applied).
	Truncated bool `json:"truncated,omitempty"`
}

// PickerResolve looks up the picker source and returns matching items.
// Filtering is applied client-side after the resolver returns so
// sources don't have to implement filtering uniformly.
func (m *Ops) PickerResolve(ctx context.Context, in PickerResolveInput) (PickerResolveResult, error) {
	source := strings.TrimSpace(in.Source)
	if source == "" {
		return PickerResolveResult{}, fmt.Errorf("source is required")
	}
	if m.Pickers == nil {
		return PickerResolveResult{}, fmt.Errorf("picker registry not configured")
	}
	fn, ok := m.Pickers.Get(source)
	if !ok {
		avail := strings.Join(m.Pickers.Sources(), ", ")
		if avail == "" {
			return PickerResolveResult{}, fmt.Errorf("unknown picker source %q (no sources registered)", source)
		}
		return PickerResolveResult{}, fmt.Errorf("unknown picker source %q (available: %s)", source, avail)
	}
	items, err := fn(ctx, in.Query)
	if err != nil {
		return PickerResolveResult{}, fmt.Errorf("resolve %s: %w", source, err)
	}
	// Apply substring filter when the resolver didn't pre-filter.
	filtered := items
	if q := strings.ToLower(strings.TrimSpace(in.Query)); q != "" {
		filtered = filtered[:0]
		for _, it := range items {
			if strings.Contains(strings.ToLower(it.Name), q) ||
				strings.Contains(strings.ToLower(it.ID), q) {
				filtered = append(filtered, it)
			}
		}
	}
	// Apply limit.
	truncated := false
	if in.Limit > 0 && len(filtered) > in.Limit {
		filtered = filtered[:in.Limit]
		truncated = true
	}
	return PickerResolveResult{Items: filtered, Truncated: truncated}, nil
}
