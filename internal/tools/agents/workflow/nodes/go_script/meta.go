// Package go_script is the editor-side module for the `go_script`
// workflow node — palette entry, drawflow codec, inspector partial +
// JS module (Ace editor). Pairs with the engine-side executor in
// internal/agents/workflow/nodes/go_script.go.
package go_script

import (
	"github.com/a-h/templ"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	registry "github.com/yogasw/wick/internal/tools/agents/workflow/nodes"
)

type module struct{}

func init() { registry.Register(&module{}) }

func (m *module) NodeType() wf.NodeType { return wf.NodeGoScript }

func (m *module) PaletteSection() string { return "Logic" }

func (m *module) PaletteItem() registry.PaletteItem {
	return registry.PaletteItem{
		Type:  string(wf.NodeGoScript),
		Label: "go_script",
		Dot:   "bg-cyan-500",
		Hint:  "stdin → stdout JSON",
	}
}

func (m *module) Render() registry.NodeRender {
	return registry.NodeRender{
		Head:    "go_script",
		Hint:    "stdin → stdout JSON",
		CSSType: "go_script",
		Inputs:  1,
		Outputs: 1,
	}
}

// DrawflowDataFromYAML projects wf.Node fields into the inspector
// blob. Only `code` and `timeout_sec` are persistent — no mode/return
// knobs (script speaks JSON over stdio).
func (m *module) DrawflowDataFromYAML(n wf.Node) map[string]any {
	data := map[string]any{"code": n.Code}
	if n.TimeoutSec > 0 {
		data["timeout_sec"] = n.TimeoutSec
	}
	if len(n.ArgModes) > 0 {
		data["__arg_modes"] = n.ArgModes
	}
	return data
}

// YAMLFromDrawflowData is the inverse — read inspector state back
// into a wf.Node.
func (m *module) YAMLFromDrawflowData(id string, inner map[string]any) wf.Node {
	n := wf.Node{ID: id, Type: wf.NodeGoScript}
	n.Code, _ = inner["code"].(string)
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

func (m *module) InspectorScript() string { return "go_script/inspector.js" }

// stringMap coerces a canvas-saved map value into map[string]string.
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
