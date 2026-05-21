package classify

import (
	"github.com/a-h/templ"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	registry "github.com/yogasw/wick/internal/tools/agents/workflow/nodes"
)

type module struct{}

func init() { registry.Register(&module{}) }

func (m *module) NodeType() wf.NodeType { return wf.NodeClassify }

func (m *module) PaletteSection() string { return "AI" }

func (m *module) PaletteItem() registry.PaletteItem {
	return registry.PaletteItem{
		Type:  string(wf.NodeClassify),
		Label: "classify",
		Dot:   "bg-pink-500",
		Hint:  "LLM label picker",
	}
}

func (m *module) Render() registry.NodeRender {
	return registry.NodeRender{
		Head:    "classify",
		Hint:    "LLM label picker",
		CSSType: "classify",
		Inputs:  1,
		Outputs: 1,
	}
}

func (m *module) DrawflowDataFromYAML(n wf.Node) map[string]any {
	oc := ""
	for i, c := range n.OutputCases {
		if i > 0 {
			oc += "\n"
		}
		oc += c
	}
	return map[string]any{
		"output_cases": oc,
		"input":        n.Input,
		"provider":     n.Provider,
		"prompt_file":  n.PromptFile,
		"fuzzy_match":  n.FuzzyMatch,
	}
}

func (m *module) YAMLFromDrawflowData(id string, inner map[string]any) wf.Node {
	n := wf.Node{ID: id, Type: wf.NodeClassify}
	if oc, _ := inner["output_cases"].(string); oc != "" {
		n.OutputCases = []string{oc}
	}
	n.Input, _ = inner["input"].(string)
	n.Provider, _ = inner["provider"].(string)
	n.PromptFile, _ = inner["prompt_file"].(string)
	n.FuzzyMatch, _ = inner["fuzzy_match"].(bool)
	return n
}

func (m *module) InspectorPartial() templ.Component { return Inspector() }
func (m *module) InspectorScript() string           { return "" }
