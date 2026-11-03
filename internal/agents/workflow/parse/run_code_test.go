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
