package nodes

import (
	"context"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
)

type endSchema struct {
	Result string `wick:"key=result;desc=Final result template expression stored in {{.Run.final_result}}"`
}

func (e *EndExecutor) Descriptor() engine.NodeDescriptor {
	return engine.NodeDescriptor{
		Category:    engine.CategoryLogic,
		Label:       "End",
		Badge:       "halt",
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

// Execute captures n.Result (pre-rendered by engine).
func (e *EndExecutor) Execute(ctx context.Context, n workflow.Node, _ *workflow.RunContext) (workflow.NodeOutput, error) {
	return workflow.NodeOutput{Result: n.Result, Fields: map[string]any{"result": n.Result}}, nil
}

// EndSchema is the exported form of endSchema for the editor UI.
type EndSchema = endSchema
