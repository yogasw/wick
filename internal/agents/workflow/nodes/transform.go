package nodes

import (
	"context"
	"fmt"

	"github.com/yogasw/wick/internal/agents/workflow"
	wfengine "github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
)

type transformSchema struct {
	Engine     string `wick:"required;key=engine;dropdown=gotemplate|jsonpath|jq;desc=Transform engine"`
	Expression string `wick:"required;key=expression;textarea;desc=Transform expression rendered against RenderCtx"`
	Input      string `wick:"key=input;desc=Input expression (optional, defaults to full RenderCtx)"`
}

func (e *TransformExecutor) Descriptor() wfengine.NodeDescriptor {
	return wfengine.NodeDescriptor{
		Category:    wfengine.CategoryData,
		Label:       "Transform",
		Badge:       "reshape data",
		Description: "Pure-function transform via gotemplate/jsonpath/jq.",
		WhenToUse:   "Reshape data between nodes without an LLM.",
		Example:     "{\n  \"id\": \"build\",\n  \"type\": \"transform\",\n  \"engine\": \"gotemplate\",\n  \"expression\": \"{{index .Event.Payload \\\"text\\\" | upper}}\"\n}",
		Schema:      integration.StructSchema(transformSchema{}),
		Output:      map[string]string{"result": "string — rendered output"},
	}
}

// TransformExecutor runs an in-process transform on an input value.
//
//   - gotemplate (default) — Go template render
//   - jsonpath              — minimal walker (placeholder)
//   - jq                    — not implemented in this build
type TransformExecutor struct{}

// NewTransformExecutor builds the transform executor.
func NewTransformExecutor() *TransformExecutor { return &TransformExecutor{} }

// Execute runs the transform. Fields are pre-rendered by the engine
// before this call — arg_modes are respected upstream.
func (e *TransformExecutor) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	eng := n.Engine
	if eng == "" {
		eng = "gotemplate"
	}
	switch eng {
	case "gotemplate":
		out := n.Expression
		return workflow.NodeOutput{Result: out, Fields: map[string]any{"result": out}}, nil
	case "jsonpath":
		out := n.Input
		return workflow.NodeOutput{Result: out, Fields: map[string]any{"result": out}}, nil
	case "jq":
		return workflow.NodeOutput{}, fmt.Errorf("transform jq: not implemented in this build")
	default:
		return workflow.NodeOutput{}, fmt.Errorf("transform: unknown engine %q", eng)
	}
}

// TransformSchema is the exported form of transformSchema for the editor UI.
type TransformSchema = transformSchema
