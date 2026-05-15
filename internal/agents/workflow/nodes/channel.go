package nodes

import (
	"context"
	"fmt"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
)

// ChannelExecutor dispatches `type: channel` action nodes through the
// integration registry. Each registered (channel, action) pair
// describes its own input schema and Execute closure, so this executor
// is just glue: render args → look up descriptor → call Execute.
//
// Adding a new outbound op = drop a file under
// internal/agents/channels/<name>/workflow/ that registers an
// ActionDescriptor. No engine change required.
type ChannelExecutor struct {
	Registry *integration.Registry
}

// NewChannelExecutor wires the executor to the integration registry.
func NewChannelExecutor(reg *integration.Registry) *ChannelExecutor {
	return &ChannelExecutor{Registry: reg}
}

// Execute renders the node's args, resolves the descriptor by
// "<channel>.<op>", and dispatches.
func (e *ChannelExecutor) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	if e.Registry == nil {
		return workflow.NodeOutput{}, fmt.Errorf("channel executor: no integration registry")
	}
	key := n.ChannelName + "." + n.Op
	desc, ok := e.Registry.Action(key)
	if !ok {
		return workflow.NodeOutput{}, fmt.Errorf("channel action %q not registered", key)
	}
	args, err := renderArgsWithModes(n.Args, n.ArgModes, rc)
	if err != nil {
		return workflow.NodeOutput{}, fmt.Errorf("render args: %w", err)
	}
	result, err := desc.Execute(ctx, args)
	if err != nil {
		return workflow.NodeOutput{}, fmt.Errorf("%s: %w", key, err)
	}
	out := workflow.NodeOutput{Result: result, Fields: map[string]any{"result": result}}
	if m, ok := result.(map[string]any); ok {
		for k, v := range m {
			out.Fields[k] = v
		}
	}
	return out, nil
}
