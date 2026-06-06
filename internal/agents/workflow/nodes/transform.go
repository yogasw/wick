package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/itchyny/gojq"

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
		Description: "Pure-function transform: gotemplate render, or a jq program over the input JSON (jsonpath not yet supported).",
		WhenToUse:   "Reshape data between nodes without an LLM. Use jq to query/restructure JSON; gotemplate for string templating.",
		Example:     "{\n  \"id\": \"build\",\n  \"type\": \"transform\",\n  \"engine\": \"jq\",\n  \"input\": \"{{.Node.fetch.body}}\",\n  \"expression\": \"{items: [.data[] | {id, name}]}\"\n}",
		Schema:      integration.StructSchema(transformSchema{}),
		Output:      map[string]string{"result": "any — jq output (single value or array) or the rendered gotemplate string"},
	}
}

// TransformExecutor runs an in-process transform on an input value.
//
//   - gotemplate (default) — Go template render (pre-rendered upstream)
//   - jq                    — gojq program over Input JSON (or RenderCtx)
//   - jsonpath              — not implemented; use jq
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
	case "jq":
		return e.runJQ(ctx, n, rc)
	case "jsonpath":
		return workflow.NodeOutput{}, fmt.Errorf("transform jsonpath: not implemented — use the jq engine")
	default:
		return workflow.NodeOutput{}, fmt.Errorf("transform: unknown engine %q", eng)
	}
}

// runJQ runs the jq program in Expression over the Input JSON, defaulting
// to the full RenderCtx when Input is blank. A single output is returned
// bare; multiple outputs are collected into a slice.
func (e *TransformExecutor) runJQ(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	query, err := gojq.Parse(n.Expression)
	if err != nil {
		return workflow.NodeOutput{}, fmt.Errorf("transform jq: parse %q: %w", n.Expression, err)
	}
	var input any
	if strings.TrimSpace(n.Input) == "" {
		raw, err := json.Marshal(rc.RenderCtx())
		if err != nil {
			return workflow.NodeOutput{}, fmt.Errorf("transform jq: marshal context: %w", err)
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return workflow.NodeOutput{}, fmt.Errorf("transform jq: decode context: %w", err)
		}
	} else if err := json.Unmarshal([]byte(n.Input), &input); err != nil {
		return workflow.NodeOutput{}, fmt.Errorf("transform jq: input is not JSON: %w", err)
	}
	results := []any{}
	iter := query.RunWithContext(ctx, input)
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if verr, ok := v.(error); ok {
			return workflow.NodeOutput{}, fmt.Errorf("transform jq: %w", verr)
		}
		results = append(results, v)
	}
	var out any
	switch len(results) {
	case 0:
		out = nil
	case 1:
		out = results[0]
	default:
		out = results
	}
	return workflow.NodeOutput{Result: out, Fields: map[string]any{"result": out}}, nil
}

// TransformSchema is the exported form of transformSchema for the editor UI.
type TransformSchema = transformSchema
