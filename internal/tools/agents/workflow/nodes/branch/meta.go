package branch

import (
	"github.com/a-h/templ"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	registry "github.com/yogasw/wick/internal/tools/agents/workflow/nodes"
)

type module struct{}

func init() { registry.Register(&module{}) }

func (m *module) NodeType() wf.NodeType { return wf.NodeBranch }

func (m *module) PaletteSection() string { return "Logic" }

func (m *module) PaletteItem() registry.PaletteItem {
	return registry.PaletteItem{
		Type:  string(wf.NodeBranch),
		Label: "branch",
		Dot:   "bg-rose-500",
		Hint:  "conditional route",
	}
}

func (m *module) Render() registry.NodeRender {
	return registry.NodeRender{
		Head:    "branch",
		Hint:    "conditional route",
		CSSType: "branch",
		Inputs:  1,
		Outputs: 1,
	}
}

func (m *module) DrawflowDataFromYAML(n wf.Node) map[string]any {
	return map[string]any{"expr": n.Expr}
}

func (m *module) YAMLFromDrawflowData(id string, inner map[string]any) wf.Node {
	expr, _ := inner["expr"].(string)
	return wf.Node{ID: id, Type: wf.NodeBranch, Expr: expr}
}

func (m *module) InspectorPartial() templ.Component { return Inspector() }
func (m *module) InspectorScript() string           { return "" }
