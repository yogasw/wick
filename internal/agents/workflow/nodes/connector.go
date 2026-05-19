package nodes

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/connector"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	pkgconnector "github.com/yogasw/wick/pkg/connector"
)

type connectorSchema struct {
	Module   string `wick:"required;key=module;desc=Connector module key"`
	Op       string `wick:"required;key=op;desc=Operation name — call workflow_integration for per-op schema"`
	Args     string `wick:"key=args;desc=Op inputs as YAML map"`
	ArgModes string `wick:"key=arg_modes;desc=Per-field mode: fixed=literal, expression=Go template render"`
}

// Dependencies surfaces "<module>.<op>" pairs to workflow_describe.
func (e *ConnectorExecutor) Dependencies(n workflow.Node) []engine.NodeDependency {
	if n.Module == "" {
		return nil
	}
	ref := n.Module
	if n.Op != "" {
		ref += "." + n.Op
	}
	return []engine.NodeDependency{{Kind: engine.DepKindConnector, Ref: ref}}
}

// TemplateableFields exposes each Args value as args.<key> for the
// describe scan.
func (e *ConnectorExecutor) TemplateableFields(n workflow.Node) map[string]string {
	out := map[string]string{}
	for k, v := range n.Args {
		if s, ok := v.(string); ok {
			out["args."+k] = s
		}
	}
	return out
}

func (e *ConnectorExecutor) Descriptor() engine.NodeDescriptor {
	return engine.NodeDescriptor{
		Description: "Invoke a registered connector operation. Call workflow_connectors for available modules.",
		WhenToUse:   "Call any registered external integration via MCP connector.",
		Schema:      integration.StructSchema(connectorSchema{}),
	}
}

// ConnectorExecutor dispatches workflow connector nodes through the
// existing connector module ExecuteFunc.
type ConnectorExecutor struct {
	Registry *connector.Registry
}

// NewConnectorExecutor wires the executor.
func NewConnectorExecutor(reg *connector.Registry) *ConnectorExecutor {
	return &ConnectorExecutor{Registry: reg}
}

// Execute invokes the resolved (module, op) Execute func.
func (e *ConnectorExecutor) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	if e.Registry == nil {
		return workflow.NodeOutput{}, fmt.Errorf("connector executor: no registry configured")
	}
	mod, ok := e.Registry.Module(n.Module)
	if !ok {
		return workflow.NodeOutput{}, fmt.Errorf("connector module %q not registered (have: %v)", n.Module, e.Registry.List())
	}
	var op *pkgconnector.Operation
	for i, o := range mod.Operations {
		if o.Key == n.Op {
			op = &mod.Operations[i]
			break
		}
	}
	if op == nil {
		return workflow.NodeOutput{}, fmt.Errorf("connector %s has no op %q", n.Module, n.Op)
	}

	argsMap, err := renderArgsWithModes(n.Args, n.ArgModes, rc)
	if err != nil {
		return workflow.NodeOutput{}, fmt.Errorf("render args: %w", err)
	}
	inputs := stringifyArgs(argsMap)
	if err := validateRequiredInputs(op, inputs); err != nil {
		return workflow.NodeOutput{}, err
	}

	creds, cerr := e.Registry.RowCreds(n.Module, n.Row)
	if cerr != nil {
		return workflow.NodeOutput{}, fmt.Errorf("resolve creds: %w", cerr)
	}

	cctx := pkgconnector.NewCtx(ctx, n.Row, creds, inputs, e.Registry.HTTPClient(), nil, nil)
	resp, execErr := op.Execute(cctx)

	e.Registry.WriteAudit(ctx, connector.RunRecord{
		WorkflowID: rc.Workflow.ID,
		RunID:      rc.RunID,
		NodeID:       n.ID,
		Module:       n.Module,
		Op:           n.Op,
		Row:          n.Row,
		Source:       "workflow",
		RequestArgs:  argsMap,
		Response:     resp,
		Status:       statusOf(execErr),
		Error:        errStr(execErr),
		Destructive:  op.Destructive,
	})
	if execErr != nil {
		return workflow.NodeOutput{}, fmt.Errorf("%s.%s: %w", n.Module, n.Op, execErr)
	}
	return workflow.NodeOutput{Result: resp, Fields: connectorResultFields(resp)}, nil
}

func stringifyArgs(args map[string]any) map[string]string {
	out := map[string]string{}
	for k, v := range args {
		switch x := v.(type) {
		case string:
			out[k] = x
		case nil:
			out[k] = ""
		default:
			data, _ := json.Marshal(x)
			out[k] = string(data)
		}
	}
	return out
}

func validateRequiredInputs(op *pkgconnector.Operation, got map[string]string) error {
	for _, in := range op.Input {
		if in.Required && got[in.Key] == "" {
			return fmt.Errorf("connector %s missing required input %q", op.Key, in.Key)
		}
	}
	return nil
}

func connectorResultFields(v any) map[string]any {
	out := map[string]any{"result": v}
	data, err := json.Marshal(v)
	if err != nil {
		return out
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return out
	}
	for k, val := range m {
		out[k] = val
	}
	return out
}

func statusOf(err error) string {
	if err == nil {
		return "success"
	}
	return "error"
}

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
