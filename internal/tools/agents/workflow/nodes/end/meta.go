package end

import (
	"github.com/a-h/templ"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	registry "github.com/yogasw/wick/internal/tools/agents/workflow/nodes"
)

type module struct{}

func init() { registry.Register(&module{}) }

func (m *module) NodeType() wf.NodeType { return wf.NodeEnd }

func (m *module) PaletteSection() string { return "Logic" }

func (m *module) PaletteItem() registry.PaletteItem {
	return registry.PaletteItem{
		Type:  string(wf.NodeEnd),
		Label: "end",
		Dot:   "bg-green-500",
		Hint:  "terminate run",
	}
}

func (m *module) Render() registry.NodeRender {
	return registry.NodeRender{
		Head:    "end",
		Hint:    "terminate run",
		CSSType: "end",
		Inputs:  1,
		Outputs: 0,
	}
}

func (m *module) DrawflowDataFromYAML(n wf.Node) map[string]any {
	return map[string]any{"result": n.Result}
}

func (m *module) YAMLFromDrawflowData(id string, inner map[string]any) wf.Node {
	result, _ := inner["result"].(string)
	return wf.Node{ID: id, Type: wf.NodeEnd, Result: result}
}

func (m *module) InspectorPartial() templ.Component { return Inspector() }
func (m *module) InspectorScript() string           { return "" }
