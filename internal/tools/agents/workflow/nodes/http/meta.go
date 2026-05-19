// Package http is the editor-side module for the `http` workflow
// node — palette entry, drawflow codec, inspector partial + JS module.
// Pairs with the engine-side executor in
// internal/agents/workflow/nodes/http.go (single source of truth for
// the runtime schema via that executor's Descriptor()).
package http

import (
	"github.com/a-h/templ"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	registry "github.com/yogasw/wick/internal/tools/agents/workflow/nodes"
)

type module struct{}

func init() { registry.Register(&module{}) }

func (m *module) NodeType() wf.NodeType { return wf.NodeHTTP }

func (m *module) PaletteSection() string { return "Action" }

func (m *module) PaletteItem() registry.PaletteItem {
	return registry.PaletteItem{
		Type:  string(wf.NodeHTTP),
		Label: "http",
		Dot:   "bg-amber-500",
		Hint:  "GET / POST",
	}
}

func (m *module) Render() registry.NodeRender {
	return registry.NodeRender{
		Head:    "http",
		Hint:    "GET / POST",
		CSSType: "http",
		Inputs:  1,
		Outputs: 1,
	}
}

func (m *module) DrawflowDataFromYAML(n wf.Node) map[string]any {
	data := map[string]any{
		"url":    n.URL,
		"method": n.Method,
	}
	if len(n.Headers) > 0 {
		data["headers"] = n.Headers
	}
	if len(n.Query) > 0 {
		data["query"] = n.Query
	}
	if n.Body != "" {
		data["body"] = n.Body
	}
	if n.ParseResponse != "" {
		data["parse_response"] = n.ParseResponse
	}
	if n.TimeoutSec > 0 {
		data["timeout_sec"] = n.TimeoutSec
	}
	if len(n.ArgModes) > 0 {
		data["__arg_modes"] = n.ArgModes
	}
	return data
}

func (m *module) YAMLFromDrawflowData(id string, inner map[string]any) wf.Node {
	n := wf.Node{ID: id, Type: wf.NodeHTTP}
	n.URL, _ = inner["url"].(string)
	n.Method, _ = inner["method"].(string)
	n.Headers = stringMap(inner["headers"])
	n.Query = stringMap(inner["query"])
	n.Body, _ = inner["body"].(string)
	n.ParseResponse, _ = inner["parse_response"].(string)
	switch v := inner["timeout_sec"].(type) {
	case int:
		n.TimeoutSec = v
	case float64:
		n.TimeoutSec = int(v)
	}
	n.ArgModes = stringMap(inner["__arg_modes"])
	return n
}

func (m *module) InspectorPartial() templ.Component { return Inspector() }

func (m *module) InspectorScript() string { return "http/inspector.js" }

// stringMap coerces a canvas-saved map value (map[string]any with
// string children, or already map[string]string) into map[string]string.
// Returns nil when the input has no usable string entries.
func stringMap(v any) map[string]string {
	switch m := v.(type) {
	case map[string]string:
		if len(m) == 0 {
			return nil
		}
		return m
	case map[string]any:
		if len(m) == 0 {
			return nil
		}
		out := make(map[string]string, len(m))
		for k, vv := range m {
			if s, ok := vv.(string); ok {
				out[k] = s
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return nil
}
