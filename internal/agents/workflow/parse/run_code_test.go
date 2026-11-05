package parse

import (
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

func errorPaths(r *Result) map[string]bool {
	out := map[string]bool{}
	for _, e := range r.Errors {
		out[e.Path] = true
	}
	return out
}

// run_code needs code and only takes ts / js.
func TestValidate_RunCode(t *testing.T) {
	ok := Validate(wfWithNode(workflow.Node{Type: workflow.NodeRunCode, Code: "return 1", Lang: "js", Input: `{"a":"{{.Event.Payload.a}}"}`}))
	if len(ok.Errors) != 0 {
		t.Fatalf("valid run_code rejected: %v", ok.Errors)
	}
	bad := errorPaths(Validate(wfWithNode(workflow.Node{Type: workflow.NodeRunCode, Lang: "py"})))
	for _, p := range []string{"graph.nodes[start].code", "graph.nodes[start].lang"} {
		if !bad[p] {
			t.Errorf("missing error at %s; got %v", p, bad)
		}
	}
}

// script_path replaces code, stays relative and needs the project it is read from.
func TestValidate_RunCodeScriptPath(t *testing.T) {
	ok := Validate(wfWithNode(workflow.Node{Type: workflow.NodeRunCode, ScriptPath: "scripts/report.ts", Workspace: "proj-1"}))
	if len(ok.Errors) != 0 {
		t.Fatalf("valid script_path rejected: %v", ok.Errors)
	}
	cases := []struct {
		n    workflow.Node
		path string
	}{
		{workflow.Node{Type: workflow.NodeRunCode, Code: "return 1", ScriptPath: "a.ts", Workspace: "proj-1"}, "graph.nodes[start].script_path"},
		{workflow.Node{Type: workflow.NodeRunCode, ScriptPath: "../a.ts", Workspace: "proj-1"}, "graph.nodes[start].script_path"},
		{workflow.Node{Type: workflow.NodeRunCode, ScriptPath: "/etc/a.ts", Workspace: "proj-1"}, "graph.nodes[start].script_path"},
		{workflow.Node{Type: workflow.NodeRunCode, ScriptPath: "a.ts"}, "graph.nodes[start].workspace"},
	}
	for _, c := range cases {
		if bad := errorPaths(Validate(wfWithNode(c.n))); !bad[c.path] {
			t.Errorf("%+v: missing error at %s; got %v", c.n, c.path, bad)
		}
	}
}

// Inline code using wick.file needs the project whose codemode/ it uses.
func TestValidate_RunCodeFileNeedsWorkspace(t *testing.T) {
	code := `return wick.file.readJSON("items.json")`
	if bad := errorPaths(Validate(wfWithNode(workflow.Node{Type: workflow.NodeRunCode, Code: code}))); !bad["graph.nodes[start].workspace"] {
		t.Fatalf("missing workspace error; got %v", bad)
	}
	if ok := Validate(wfWithNode(workflow.Node{Type: workflow.NodeRunCode, Code: code, Workspace: "proj-1"})); len(ok.Errors) != 0 {
		t.Fatalf("valid run_code rejected: %v", ok.Errors)
	}
}
