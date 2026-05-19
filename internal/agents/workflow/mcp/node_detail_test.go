package mcp

import (
	"context"
	"strings"
	"testing"

	pkgconnector "github.com/yogasw/wick/pkg/connector"
	pkgentity "github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/wickdocs"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/connector"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
)

// fakeExecutor is a minimal workflow.Executor + engine.Describer used
// to seed Engine.Descriptors for built-in node tests.
type fakeExecutor struct {
	desc engine.NodeDescriptor
}

func (f *fakeExecutor) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	return workflow.NodeOutput{}, nil
}

func (f *fakeExecutor) Descriptor() engine.NodeDescriptor { return f.desc }

// newTestOps builds an Ops bag with only the registries NodeDetail
// touches — engine, integration, connectors, plus the trigger registry
// that lives on engine. No service / canvas / state — none of the
// detail branches reach those.
func newTestOps(t *testing.T) *Ops {
	t.Helper()
	eng := &engine.Engine{
		Executors:   map[workflow.NodeType]workflow.Executor{},
		Descriptors: map[workflow.NodeType]engine.NodeDescriptor{},
		Triggers:    engine.NewTriggerRegistry(),
	}
	eng.Triggers.RegisterMany(engine.DefaultTriggerDescriptors()...)

	// Seed a built-in node descriptor with Docs so the projector has
	// something to lift.
	eng.RegisterWithDesc("test_node", &fakeExecutor{}, engine.NodeDescriptor{
		Type:        "test_node",
		Description: "Test built-in node",
		WhenToUse:   "Unit tests",
		Schema:      map[string]any{"type": "object"},
		Output:      map[string]string{"result": "test output"},
		Docs: wickdocs.Docs{
			Quirks:   []string{"test quirk"},
			Examples: []wickdocs.Example{{Name: "basic", YAML: "- id: t\n  type: test_node"}},
		},
	})

	ir := integration.New()
	// Event
	ir.RegisterEvent(integration.EventDescriptor{
		Channel:     "demo",
		Event:       "ping",
		Name:        "Demo: Ping",
		Description: "Test event",
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{"payload.x": "demo"},
		},
	})
	// Action
	ir.RegisterAction(integration.ActionDescriptor{
		Channel:     "demo",
		Action:      "shout",
		Name:        "Demo: Shout",
		Description: "Test action",
		Destructive: true,
		Docs: wickdocs.Docs{
			Quirks: []string{"shouts loudly"},
		},
	})

	cr := connector.NewRegistry(nil, nil)
	cr.Register(pkgconnector.Module{
		Meta: pkgconnector.Meta{Key: "demo", Name: "Demo"},
		Operations: []pkgconnector.Operation{
			{
				Key:         "echo",
				Name:        "Echo",
				Description: "Echo a value",
				Input: []pkgentity.Config{
					{Key: "text", Description: "value to echo", Required: true, Type: "text"},
				},
				Docs: wickdocs.Docs{
					CommonPitfalls: []string{"Don't echo secrets"},
				},
			},
		},
	})

	return &Ops{
		Engine:      eng,
		Integration: ir,
		Connectors:  cr,
	}
}

func TestNodeDetailEmptyKey(t *testing.T) {
	m := newTestOps(t)
	if _, err := m.NodeDetail(""); err == nil {
		t.Fatalf("NodeDetail(\"\") err=nil, want error")
	}
}

func TestNodeDetailBuiltIn(t *testing.T) {
	m := newTestOps(t)
	got, err := m.NodeDetail("test_node")
	if err != nil {
		t.Fatalf("NodeDetail: %v", err)
	}
	if got.Kind != KindBuiltIn {
		t.Fatalf("Kind = %q, want %q", got.Kind, KindBuiltIn)
	}
	if got.Description != "Test built-in node" {
		t.Fatalf("Description = %q", got.Description)
	}
	if len(got.Quirks) != 1 || got.Quirks[0] != "test quirk" {
		t.Fatalf("Quirks not projected: %+v", got.Quirks)
	}
	if got.Output["result"] != "test output" {
		t.Fatalf("Output[result] = %v, want \"test output\"", got.Output["result"])
	}
}

func TestNodeDetailBuiltInUnknown(t *testing.T) {
	m := newTestOps(t)
	if _, err := m.NodeDetail("nope"); err == nil {
		t.Fatalf("unknown built-in type didn't error")
	}
}

func TestNodeDetailChannelEvent(t *testing.T) {
	m := newTestOps(t)
	got, err := m.NodeDetail("channel:demo.ping")
	if err != nil {
		t.Fatalf("NodeDetail: %v", err)
	}
	if got.Kind != KindChannelEvent {
		t.Fatalf("Kind = %q, want %q", got.Kind, KindChannelEvent)
	}
	if got.OutputShape["payload.x"] != "demo" {
		t.Fatalf("OutputShape not projected: %+v", got.OutputShape)
	}
}

func TestNodeDetailChannelAction(t *testing.T) {
	m := newTestOps(t)
	got, err := m.NodeDetail("channel:demo.shout")
	if err != nil {
		t.Fatalf("NodeDetail: %v", err)
	}
	if got.Kind != KindChannelAction {
		t.Fatalf("Kind = %q, want %q", got.Kind, KindChannelAction)
	}
	if !got.Destructive {
		t.Fatalf("Destructive flag dropped")
	}
	if len(got.Quirks) != 1 {
		t.Fatalf("Quirks not projected: %+v", got.Quirks)
	}
}

func TestNodeDetailChannelUnknown(t *testing.T) {
	m := newTestOps(t)
	if _, err := m.NodeDetail("channel:demo.missing"); err == nil {
		t.Fatalf("unknown channel node didn't error")
	}
}

func TestNodeDetailConnectorOp(t *testing.T) {
	m := newTestOps(t)
	got, err := m.NodeDetail("connector:demo.echo")
	if err != nil {
		t.Fatalf("NodeDetail: %v", err)
	}
	if got.Kind != KindConnectorOp {
		t.Fatalf("Kind = %q, want %q", got.Kind, KindConnectorOp)
	}
	if got.Schema == nil {
		t.Fatalf("Schema nil — expected projection from connector Input")
	}
	if len(got.CommonPitfalls) != 1 {
		t.Fatalf("CommonPitfalls not projected: %+v", got.CommonPitfalls)
	}
}

func TestNodeDetailConnectorMalformed(t *testing.T) {
	m := newTestOps(t)
	if _, err := m.NodeDetail("connector:demo-echo"); err == nil {
		t.Fatalf("connector key without dot didn't error")
	}
}

func TestNodeDetailConnectorUnknownModule(t *testing.T) {
	m := newTestOps(t)
	if _, err := m.NodeDetail("connector:nope.echo"); err == nil {
		t.Fatalf("unknown connector module didn't error")
	}
}

func TestNodeDetailConnectorUnknownOp(t *testing.T) {
	m := newTestOps(t)
	if _, err := m.NodeDetail("connector:demo.nope"); err == nil {
		t.Fatalf("unknown connector op didn't error")
	}
}

func TestNodeDetailTrigger(t *testing.T) {
	m := newTestOps(t)
	got, err := m.NodeDetail("trigger:channel")
	if err != nil {
		t.Fatalf("NodeDetail: %v", err)
	}
	if got.Kind != KindTrigger {
		t.Fatalf("Kind = %q, want %q", got.Kind, KindTrigger)
	}
	if !strings.Contains(got.Description, "channel event") {
		t.Fatalf("Description = %q, expected channel-event wording", got.Description)
	}
	if got.Docs.IsZero() {
		t.Fatalf("trigger:channel projected with empty Docs — Default catalog should attach Docs")
	}
}

func TestNodeDetailTriggerUnknown(t *testing.T) {
	m := newTestOps(t)
	if _, err := m.NodeDetail("trigger:unicorn"); err == nil {
		t.Fatalf("unknown trigger didn't error")
	}
}

func TestNodeDetailIOSamplesProject(t *testing.T) {
	// Built-in node with InputSample + OutputSample should round-trip
	// both into the unified detail response.
	m := newTestOps(t)
	m.Engine.RegisterWithDesc("sampled", &fakeExecutor{}, engine.NodeDescriptor{
		Description: "Sampled",
		Docs: wickdocs.Docs{
			InputSample:  `{"a":1}`,
			OutputSample: `{"b":2}`,
		},
	})
	got, err := m.NodeDetail("sampled")
	if err != nil {
		t.Fatalf("NodeDetail: %v", err)
	}
	if got.InputSample != `{"a":1}` {
		t.Fatalf("InputSample = %q", got.InputSample)
	}
	if got.OutputSample != `{"b":2}` {
		t.Fatalf("OutputSample = %q", got.OutputSample)
	}
}

func TestNodeDetailDocsEmbedJSON(t *testing.T) {
	// Smoke-test: empty Docs marshals without leaking `quirks: []`,
	// `templateable_fields: null`, etc. This catches accidental
	// drops of the omitempty tag.
	m := newTestOps(t)
	// Register a doc-less built-in.
	m.Engine.RegisterWithDesc("plain", &fakeExecutor{}, engine.NodeDescriptor{
		Description: "Plain",
	})
	got, err := m.NodeDetail("plain")
	if err != nil {
		t.Fatalf("NodeDetail: %v", err)
	}
	if !got.Docs.IsZero() {
		t.Fatalf("plain node projected with non-empty Docs: %+v", got.Docs)
	}
}
