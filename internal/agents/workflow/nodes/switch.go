package nodes

import (
	"context"
	"fmt"
	"strings"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/agents/workflow/template"
)

// switchSchema is a placeholder for the MCP catalog — the real
// rule editor is hand-coded in the inspector (custom rows builder),
// not the reflected ArgForm. Schema still surfaces the description so
// AI knows to populate `cases` and `default_case` on the YAML node.
type switchSchema struct {
	Cases       string `wick:"required;key=cases;textarea;desc=List of {when, case} rules. First rule whose 'when' is true wins; engine emits Verdict=case so edges with matching case: route downstream. UI uses a rows builder."`
	DefaultCase string `wick:"key=default_case;desc=Verdict to emit when no rule matches. Leave blank to fail closed."`
}

// SwitchExecutor evaluates each rule's `when` expression in order;
// the first rule that returns true sets Verdict to its `case` label.
// Falls back to DefaultCase when nothing matches. Same matching
// semantics as `branch` (binary ops or truthy string).
type SwitchExecutor struct{}

// NewSwitchExecutor wires the executor.
func NewSwitchExecutor() *SwitchExecutor { return &SwitchExecutor{} }

// Descriptor exposes schema + docs for the MCP catalog.
func (e *SwitchExecutor) Descriptor() engine.NodeDescriptor {
	return engine.NodeDescriptor{
		Description: "Multi-case branching. First rule whose 'when' is true wins; emits Verdict=case so downstream edges route by case:.",
		WhenToUse:   "Routing with 2+ conditions where 'branch' (single expr) is awkward. Each rule is independent; ordering matters (first match wins).",
		Example:     "- id: route\n  type: switch\n  cases:\n    - when: '{{index .Event.Payload \"status\"}} == \"approved\"'\n      case: approve\n    - when: '{{index .Event.Payload \"status\"}} == \"rejected\"'\n      case: reject\n  default_case: review",
		Schema:      integration.StructSchema(switchSchema{}),
		Output: map[string]string{
			"verdict": "string — winning case label (or default_case fallback)",
		},
	}
}

// Execute walks cases in order; returns the first match's Case label
// as Verdict. When no rule wins and DefaultCase is set, that label is
// emitted. Empty DefaultCase with no match = error (fail closed) so
// the workflow halts instead of silently dropping the run.
func (e *SwitchExecutor) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	if len(n.Cases) == 0 {
		return workflow.NodeOutput{}, fmt.Errorf("switch %q: no cases configured", n.ID)
	}
	rctx := rc.RenderCtx()
	for i, rule := range n.Cases {
		when := strings.TrimSpace(rule.When)
		label := strings.TrimSpace(rule.Case)
		if label == "" {
			return workflow.NodeOutput{}, fmt.Errorf("switch %q: case[%d] has empty case label", n.ID, i)
		}
		matched, err := evalSwitchRule(when, rctx)
		if err != nil {
			return workflow.NodeOutput{}, fmt.Errorf("switch %q: case[%d] %q: %w", n.ID, i, label, err)
		}
		if matched {
			return workflow.NodeOutput{Verdict: label, Result: label}, nil
		}
	}
	if def := strings.TrimSpace(n.DefaultCase); def != "" {
		return workflow.NodeOutput{Verdict: def, Result: def}, nil
	}
	return workflow.NodeOutput{}, fmt.Errorf("switch %q: no case matched and default_case is empty", n.ID)
}

// evalSwitchRule reuses branch.go's binary-comparison helper for
// "<expr> <op> <expr>" rules; falls back to rendering the template
// and treating a non-empty trimmed result as truthy. Empty `when`
// counts as a catch-all (rare but legal — typically reserved for the
// last rule before DefaultCase).
func evalSwitchRule(when string, rctx workflow.RenderCtx) (bool, error) {
	if when == "" {
		return true, nil
	}
	if v, isBool, err := evalBoolExpr(when, rctx); err != nil {
		return false, err
	} else if isBool {
		return v, nil
	}
	rendered, err := template.Render(when, rctx)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(rendered)) {
	case "", "false", "0", "no", "off":
		return false, nil
	}
	return true, nil
}
