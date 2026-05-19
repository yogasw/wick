package nodes

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
)

type channelSchema struct {
	ChannelName string `wick:"required;key=channel;desc=Channel module name (e.g. slack)"`
	Op          string `wick:"required;key=op;desc=Action name — call workflow_integration to list available ops per channel"`
	Args        string `wick:"key=args;desc=Op inputs as YAML map — see workflow_integration for exact schema per op"`
	ArgModes    string `wick:"key=arg_modes;desc=Per-field mode: fixed=literal value, expression=Go template render (default)"`
}

// Dependencies surfaces (channel, action) pairs through
// workflow_describe so impact analysis sees the exact op the node
// invokes — not just the channel module name.
func (e *ChannelExecutor) Dependencies(n workflow.Node) []engine.NodeDependency {
	if n.ChannelName == "" {
		return nil
	}
	ref := n.ChannelName
	if n.Op != "" {
		ref += "." + n.Op
	}
	return []engine.NodeDependency{{Kind: engine.DepKindChannel, Ref: ref}}
}

// TemplateableFields surfaces the per-arg values so describe's
// template cross-ref scan reaches them. Each Args entry is exposed as
// args.<key> in issue paths so the warning shows which arg referenced
// an undeclared node.
func (e *ChannelExecutor) TemplateableFields(n workflow.Node) map[string]string {
	out := map[string]string{}
	for k, v := range n.Args {
		if s, ok := v.(string); ok {
			out["args."+k] = s
		}
	}
	return out
}

func (e *ChannelExecutor) Descriptor() engine.NodeDescriptor {
	return engine.NodeDescriptor{
		Description: "Invoke a channel action (send_message, open_modal, …). Call workflow_integration for available ops + schemas.",
		WhenToUse:   "Send messages, open modals, react, reply via Slack/Telegram/etc.",
		Example:     "- id: sendmsg\n  type: channel\n  channel: slack\n  op: send_message\n  args:\n    channel: '{{index .Event.Payload \"channel_id\"}}'\n    text: Hello\n  arg_modes:\n    channel: expression\n    text: fixed",
		Schema:      integration.StructSchema(channelSchema{}),
		Output: map[string]string{
			"ts":        "channel-dependent (Slack: message timestamp)",
			"channel":   "channel ID",
			"view_id":   "open_modal only",
			"view_hash": "open_modal only",
		},
	}
}

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
	// Convert typed struct → map[string]any via JSON tags so templates
	// can drill into `.result.<json-tag>` (Go text/template can't reflect
	// into an `interface{}` to reach struct fields by tag name).
	flat := flattenFields(result)
	var resultVal any = flat
	if flat == nil {
		resultVal = result
	}
	out := workflow.NodeOutput{Result: resultVal, Fields: map[string]any{"result": resultVal}}
	for k, v := range flat {
		out.Fields[k] = v
	}
	return out, nil
}

// flattenFields turns an action's typed output (struct or map) into a
// flat map[string]any keyed by JSON tag, so templates can reach each
// field via {{.Node.<id>.<field>}}. Without this, struct outputs only
// expose `result` and downstream templates fail with "map has no entry
// for key" on every named field.
func flattenFields(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil
	}
	return out
}
