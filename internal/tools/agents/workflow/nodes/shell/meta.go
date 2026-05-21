package shell

import (
	"github.com/a-h/templ"

	wf "github.com/yogasw/wick/internal/agents/workflow"
	registry "github.com/yogasw/wick/internal/tools/agents/workflow/nodes"
)

type module struct{}

func init() { registry.Register(&module{}) }

func (m *module) NodeType() wf.NodeType { return wf.NodeShell }

func (m *module) PaletteSection() string { return "Action" }

func (m *module) PaletteItem() registry.PaletteItem {
	return registry.PaletteItem{
		Type:  string(wf.NodeShell),
		Label: "shell",
		Dot:   "bg-slate-500",
		Hint:  "run command",
	}
}

func (m *module) Render() registry.NodeRender {
	return registry.NodeRender{
		Head:    "shell",
		Hint:    "run command",
		CSSType: "shell",
		Inputs:  1,
		Outputs: 1,
	}
}

func (m *module) DrawflowDataFromYAML(n wf.Node) map[string]any {
	cmd := ""
	if len(n.Command) == 1 {
		cmd = n.Command[0]
	} else if len(n.Command) > 1 {
		for i, c := range n.Command {
			if i > 0 {
				cmd += " "
			}
			cmd += c
		}
	}
	env := ""
	for k, v := range n.ShellEnv {
		if env != "" {
			env += "\n"
		}
		env += k + ": " + v
	}
	return map[string]any{
		"command":      cmd,
		"env":          env,
		"cwd":          n.Cwd,
		"parse_output": n.ParseOutput,
		"timeout_sec":  n.TimeoutSec,
	}
}

func (m *module) YAMLFromDrawflowData(id string, inner map[string]any) wf.Node {
	n := wf.Node{ID: id, Type: wf.NodeShell}
	if cmd, _ := inner["command"].(string); cmd != "" {
		n.Command = []string{cmd}
	}
	n.Cwd, _ = inner["cwd"].(string)
	n.ParseOutput, _ = inner["parse_output"].(string)
	switch v := inner["timeout_sec"].(type) {
	case int:
		n.TimeoutSec = v
	case float64:
		n.TimeoutSec = int(v)
	}
	return n
}

func (m *module) InspectorPartial() templ.Component { return Inspector() }
func (m *module) InspectorScript() string           { return "" }
