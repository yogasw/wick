package parse

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
)

// wfWithNode wraps a single node in the smallest workflow that otherwise
// passes Validate, so any error we see comes from the node body itself.
func wfWithNode(n workflow.Node) workflow.Workflow {
	n.ID = "start"
	return workflow.Workflow{
		ID:       "wf-argmodes",
		Name:     "argmodes",
		Triggers: []workflow.Trigger{{Type: workflow.TriggerManual, EntryNode: "start"}},
		Graph:    workflow.Graph{Nodes: []workflow.Node{n}},
	}
}

// hasArgModeError reports whether Validate flagged an arg_modes mismatch
// as a publish-blocking Error (not merely a warning).
func hasArgModeError(r *Result) bool {
	for _, e := range r.Errors {
		if strings.Contains(e.Path, "arg_modes") {
			return true
		}
	}
	return false
}

// fixed mode + a {{...}} value must block publish — the template would
// never render, so it's an Error, not a warning.
func TestValidate_FixedWithTemplate_BlocksPublish(t *testing.T) {
	cases := []struct {
		name string
		node workflow.Node
	}{
		{"http_url", workflow.Node{
			Type: workflow.NodeHTTP, Method: "POST",
			URL:      "https://api.github.com/repos/{{.Event.Payload.owner}}/x/merge",
			ArgModes: map[string]string{"url": "fixed"},
		}},
		{"http_body", workflow.Node{
			Type: workflow.NodeHTTP, Method: "POST", URL: "https://abc.com/x",
			Body:     `{"id":"{{.Event.Payload.id}}"}`,
			ArgModes: map[string]string{"body": "fixed"},
		}},
		{"agent_prompt", workflow.Node{
			Type: workflow.NodeAgent, Prompt: "summarize {{.Event.Payload.text}}",
			ArgModes: map[string]string{"prompt": "fixed"},
		}},
		{"connector_arg", workflow.Node{
			Type: workflow.NodeConnector, Module: "github", Op: "merge_pr",
			Args:     map[string]any{"pr": "{{.Event.Payload.pr_number}}"},
			ArgModes: map[string]string{"pr": "fixed"},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := Validate(wfWithNode(c.node))
			if r.Ok() {
				t.Fatalf("expected publish-blocking error, got Ok(); warnings=%v", r.Warnings)
			}
			if !hasArgModeError(r) {
				t.Fatalf("expected an arg_modes Error, got errors=%v", r.Errors)
			}
		})
	}
}

// expression mode on the same {{...}} value is correct — no arg_modes error.
func TestValidate_ExpressionWithTemplate_Allowed(t *testing.T) {
	n := workflow.Node{
		Type: workflow.NodeHTTP, Method: "POST",
		URL:      "https://api.github.com/repos/{{.Event.Payload.owner}}/x/merge",
		ArgModes: map[string]string{"url": "expression"},
	}
	r := Validate(wfWithNode(n))
	if hasArgModeError(r) {
		t.Fatalf("expression mode must not error on a template value; errors=%v", r.Errors)
	}
}

// fixed mode on a plain literal (no {{) is fine — that's the whole point
// of fixed mode.
func TestValidate_FixedLiteral_Allowed(t *testing.T) {
	n := workflow.Node{
		Type: workflow.NodeHTTP, Method: "GET",
		URL:      "https://abc.com/static",
		ArgModes: map[string]string{"url": "fixed"},
	}
	r := Validate(wfWithNode(n))
	if hasArgModeError(r) {
		t.Fatalf("fixed literal must not error; errors=%v", r.Errors)
	}
}
