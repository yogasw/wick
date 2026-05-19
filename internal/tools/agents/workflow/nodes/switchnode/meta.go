// Package switchnode is the editor-side module for the `switch`
// workflow node — palette entry, drawflow codec, inspector partial +
// JS module. Pairs with the engine-side executor in
// internal/agents/workflow/nodes/switch.go.
//
// Package name avoids the Go `switch` keyword by adding the `node`
// suffix; user-facing strings still say "switch".
package switchnode

import (
	"github.com/a-h/templ"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	registry "github.com/yogasw/wick/internal/tools/agents/workflow/nodes"
)

type module struct{}

func init() { registry.Register(&module{}) }

func (m *module) NodeType() wf.NodeType { return wf.NodeSwitch }

func (m *module) PaletteSection() string { return "Logic" }

func (m *module) PaletteItem() registry.PaletteItem {
	return registry.PaletteItem{
		Type:  string(wf.NodeSwitch),
		Label: "switch",
		Dot:   "bg-fuchsia-500",
		Hint:  "first match wins",
	}
}

func (m *module) Render() registry.NodeRender {
	return registry.NodeRender{
		Head:    "switch",
		Hint:    "first match wins",
		CSSType: "switch",
		Inputs:  1,
		Outputs: 1,
	}
}

// DrawflowDataFromYAML projects wf.Node fields into the inspector
// blob. `cases` becomes []{when,case} maps so the rows builder can
// render them; `default_case` is plain string.
func (m *module) DrawflowDataFromYAML(n wf.Node) map[string]any {
	rows := make([]map[string]any, 0, len(n.Cases))
	for _, c := range n.Cases {
		rows = append(rows, map[string]any{"when": c.When, "case": c.Case})
	}
	data := map[string]any{"cases": rows}
	if n.DefaultCase != "" {
		data["default_case"] = n.DefaultCase
	}
	return data
}

// YAMLFromDrawflowData is the inverse. The rows builder JS posts
// `cases` as []{when,case}; we coerce each entry through the
// canvas-shape (map[string]any with string children) into the typed
// wf.SwitchCase.
func (m *module) YAMLFromDrawflowData(id string, inner map[string]any) wf.Node {
	n := wf.Node{ID: id, Type: wf.NodeSwitch}
	if raw, ok := inner["cases"].([]any); ok {
		for _, item := range raw {
			row, ok := item.(map[string]any)
			if !ok {
				continue
			}
			when, _ := row["when"].(string)
			caseLbl, _ := row["case"].(string)
			if when == "" && caseLbl == "" {
				continue
			}
			n.Cases = append(n.Cases, wf.SwitchCase{When: when, Case: caseLbl})
		}
	}
	n.DefaultCase, _ = inner["default_case"].(string)
	return n
}

func (m *module) InspectorPartial() templ.Component { return Inspector() }

func (m *module) InspectorScript() string { return "switchnode/inspector.js" }
