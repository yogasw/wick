package db_query

import (
	"github.com/a-h/templ"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	registry "github.com/yogasw/wick/internal/tools/agents/workflow/nodes"
)

type module struct{}

func init() { registry.Register(&module{}) }

func (m *module) NodeType() wf.NodeType { return wf.NodeDBQuery }

func (m *module) PaletteSection() string { return "Action" }

func (m *module) PaletteItem() registry.PaletteItem {
	return registry.PaletteItem{
		Type:  string(wf.NodeDBQuery),
		Label: "db_query",
		Dot:   "bg-sky-500",
		Hint:  "SQL query",
	}
}

func (m *module) Render() registry.NodeRender {
	return registry.NodeRender{
		Head:    "db_query",
		Hint:    "SQL query",
		CSSType: "db_query",
		Inputs:  1,
		Outputs: 1,
	}
}

func (m *module) DrawflowDataFromYAML(n wf.Node) map[string]any {
	return map[string]any{
		"database": n.Database,
		"sql":      n.SQL,
		"sql_args": func() string {
			s := ""
			for i, a := range n.SQLArgs {
				if i > 0 {
					s += "\n"
				}
				s += a
			}
			return s
		}(),
	}
}

func (m *module) YAMLFromDrawflowData(id string, inner map[string]any) wf.Node {
	n := wf.Node{ID: id, Type: wf.NodeDBQuery}
	n.Database, _ = inner["database"].(string)
	n.SQL, _ = inner["sql"].(string)
	if sa, _ := inner["sql_args"].(string); sa != "" {
		n.SQLArgs = []string{sa}
	}
	return n
}

func (m *module) InspectorPartial() templ.Component { return Inspector() }
func (m *module) InspectorScript() string           { return "" }
