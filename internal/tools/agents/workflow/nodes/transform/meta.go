package transform

import (
	"github.com/a-h/templ"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	registry "github.com/yogasw/wick/internal/tools/agents/workflow/nodes"
)

type module struct{}

func init() { registry.Register(&module{}) }

func (m *module) NodeType() wf.NodeType { return wf.NodeTransform }

func (m *module) PaletteSection() string { return "Action" }

func (m *module) PaletteItem() registry.PaletteItem {
	return registry.PaletteItem{
		Type:  string(wf.NodeTransform),
		Label: "transform",
		Dot:   "bg-lime-500",
		Hint:  "reshape data",
	}
}

func (m *module) Render() registry.NodeRender {
	return registry.NodeRender{
		Head:    "transform",
		Hint:    "reshape data",
		CSSType: "transform",
		Inputs:  1,
		Outputs: 1,
	}
}

func (m *module) DrawflowDataFromYAML(n wf.Node) map[string]any {
	return map[string]any{
		"engine":     n.Engine,
		"expression": n.Expression,
		"input":      n.Input,
	}
}

func (m *module) YAMLFromDrawflowData(id string, inner map[string]any) wf.Node {
	n := wf.Node{ID: id, Type: wf.NodeTransform}
	n.Engine, _ = inner["engine"].(string)
	n.Expression, _ = inner["expression"].(string)
	n.Input, _ = inner["input"].(string)
	return n
}

func (m *module) InspectorPartial() templ.Component { return Inspector() }
func (m *module) InspectorScript() string           { return "" }
