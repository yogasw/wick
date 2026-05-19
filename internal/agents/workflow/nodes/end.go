package nodes

import (
	"context"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/agents/workflow/template"
)

type endSchema struct {
	Result string `wick:"key=result;desc=Final result template expression stored in {{.Run.final_result}}"`
}

func (e *EndExecutor) Descriptor() engine.NodeDescriptor {
	return engine.NodeDescriptor{
		Description: "Terminator. Captures a final result template.",
		WhenToUse:   "Explicit end-of-flow with a result payload.",
		Schema:      integration.StructSchema(endSchema{}),
	}
}

// EndExecutor is the terminator. Captures n.Result so downstream
// {{.Run.final_result}}-style reads can pick it up.
type EndExecutor struct{}

// NewEndExecutor builds the end executor.
func NewEndExecutor() *EndExecutor { return &EndExecutor{} }

// Execute renders n.Result if set.
func (e *EndExecutor) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	if n.Result == "" {
		return workflow.NodeOutput{Result: ""}, nil
	}
	out, err := template.Render(n.Result, rc.RenderCtx())
	if err != nil {
		return workflow.NodeOutput{}, err
	}
	return workflow.NodeOutput{Result: out, Fields: map[string]any{"result": out}}, nil
}
