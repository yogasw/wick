package nodes

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/agents/workflow/template"
)

type branchSchema struct {
	Expr string `wick:"required;key=expr;desc=Go template expression that returns a case label string matching downstream edge case: values"`
}

func (e *BranchExecutor) Descriptor() engine.NodeDescriptor {
	return engine.NodeDescriptor{
		Description: "Evaluates a Go template expression; routes to the edge whose case: label matches the result.",
		WhenToUse:   "Routing logic is structured (no natural language).",
		Example:     "- id: route\n  type: branch\n  expr: '{{index .Event.Payload \"action_id\"}}'",
		Schema:      integration.StructSchema(branchSchema{}),
	}
}

// BranchExecutor evaluates a Go-template expression and exposes the
// result as Verdict so the engine filters outgoing edges by `case:`.
type BranchExecutor struct{}

// NewBranchExecutor constructs the branch executor.
func NewBranchExecutor() *BranchExecutor { return &BranchExecutor{} }

// Execute renders n.Expr; if it contains a binary operator, treats as
// boolean compare → "true"/"false". Otherwise the rendered string IS
// the verdict (string switch).
func (e *BranchExecutor) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	rctx := rc.RenderCtx()
	expr := strings.TrimSpace(n.Expr)
	if expr == "" {
		return workflow.NodeOutput{}, fmt.Errorf("branch %q: expr is empty", n.ID)
	}

	if v, isBool, err := evalBoolExpr(expr, rctx); err == nil && isBool {
		return workflow.NodeOutput{Verdict: boolStr(v), Result: v}, nil
	}

	rendered, err := template.Render(expr, rctx)
	if err != nil {
		return workflow.NodeOutput{}, fmt.Errorf("branch render: %w", err)
	}
	verdict := strings.TrimSpace(rendered)
	return workflow.NodeOutput{Verdict: verdict, Result: verdict}, nil
}

func evalBoolExpr(expr string, rctx workflow.RenderCtx) (bool, bool, error) {
	ops := []string{"==", "!=", "<=", ">=", "<", ">"}
	for _, op := range ops {
		idx := strings.Index(expr, op)
		if idx <= 0 {
			continue
		}
		left := strings.TrimSpace(expr[:idx])
		right := strings.TrimSpace(expr[idx+len(op):])
		leftV, err := renderOrLiteral(left, rctx)
		if err != nil {
			return false, true, err
		}
		rightV, err := renderOrLiteral(right, rctx)
		if err != nil {
			return false, true, err
		}
		return compare(leftV, rightV, op), true, nil
	}
	return false, false, nil
}

func renderOrLiteral(s string, rctx workflow.RenderCtx) (string, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`) {
		return strings.TrimSuffix(strings.TrimPrefix(s, `"`), `"`), nil
	}
	if strings.HasPrefix(s, `'`) && strings.HasSuffix(s, `'`) {
		return strings.TrimSuffix(strings.TrimPrefix(s, `'`), `'`), nil
	}
	if strings.HasPrefix(s, "{{") {
		return template.Render(s, rctx)
	}
	return s, nil
}

func compare(left, right, op string) bool {
	if l, err := strconv.ParseFloat(left, 64); err == nil {
		if r, err := strconv.ParseFloat(right, 64); err == nil {
			switch op {
			case "==":
				return l == r
			case "!=":
				return l != r
			case "<":
				return l < r
			case "<=":
				return l <= r
			case ">":
				return l > r
			case ">=":
				return l >= r
			}
		}
	}
	switch op {
	case "==":
		return left == right
	case "!=":
		return left != right
	case "<":
		return left < right
	case "<=":
		return left <= right
	case ">":
		return left > right
	case ">=":
		return left >= right
	}
	return false
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
