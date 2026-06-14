package mcp

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/service"
)

// stubService is the minimal service.Service the Describe tests need.
// Only Load is exercised — every other method returns a zero value or
// a "not implemented" error so accidental usage is loud.
type stubService struct {
	wf workflow.Workflow
}

func newStubService(wf workflow.Workflow) *stubService { return &stubService{wf: wf} }

func (s *stubService) Load(id string) (workflow.Workflow, error) { return s.wf, nil }

// Catch-all stubs — return zero values; tests that exercise these
// methods should construct a richer fake.
func (s *stubService) List() ([]string, error)                   { return []string{s.wf.ID}, nil }
func (s *stubService) Create(string, workflow.Workflow) error    { return nil }
func (s *stubService) Update(string, workflow.Workflow) error    { return nil }
func (s *stubService) Delete(string) error                       { return nil }
func (s *stubService) Toggle(string, bool) error                 { return nil }
func (s *stubService) FindByName(string, string) (string, error) { return "", nil }
func (s *stubService) LoadDraft(string) (workflow.Workflow, error) {
	return workflow.Workflow{}, service.ErrNotFound
}
func (s *stubService) HasDraft(string) bool                              { return false }
func (s *stubService) SaveDraft(string, workflow.Workflow) error         { return nil }
func (s *stubService) Publish(string, string) (workflow.Workflow, error) { return s.wf, nil }
func (s *stubService) DiscardDraft(string) error                         { return nil }
func (s *stubService) ListTests(string) ([]string, error)                { return nil, nil }
func (s *stubService) GetTest(string, string) ([]byte, error)            { return nil, fmt.Errorf("stub") }
func (s *stubService) SaveTest(string, string, []byte) error             { return nil }
func (s *stubService) DeleteTest(string, string) error                   { return nil }
func (s *stubService) LoadState(string) (workflow.WorkflowState, error) {
	return workflow.WorkflowState{}, nil
}
func (s *stubService) SaveState(string, workflow.WorkflowState) error  { return nil }
func (s *stubService) LoadEnvValues(string) (map[string]string, error) { return nil, nil }
func (s *stubService) SaveEnvValues(string, map[string]string) error   { return nil }
func (s *stubService) BaseDir() string                                 { return "" }

// stubWorkflow returns a small but representative workflow used by
// the Describe tests: 1 slack message trigger, 3 nodes
// (classify → branch → http), 2 edges.
func stubWorkflow() workflow.Workflow {
	return workflow.Workflow{
		ID:      "wf1",
		Name:    "test",
		Enabled: true,
		Triggers: []workflow.Trigger{
			{
				Type:        workflow.TriggerChannel,
				ChannelName: "slack",
				Event:       "message",
				EntryNode:   "classify",
			},
		},
		Graph: workflow.Graph{
			Entry: "classify",
			Nodes: []workflow.Node{
				{ID: "classify", Type: workflow.NodeClassify, Provider: "claude"},
				{ID: "route", Type: workflow.NodeBranch, Expr: "{{.Node.classify.verdict}}"},
				{ID: "post", Type: workflow.NodeHTTP, Method: "POST", URL: "https://example.com/{{.Node.classify.verdict}}"},
			},
			Edges: []workflow.Edge{
				{From: "classify", To: "route"},
				{From: "route", To: "post", Case: "bug"},
			},
		},
	}
}

func workflowEdge(from, to string) workflow.Edge {
	return workflow.Edge{From: from, To: to}
}

// ── TemplateTest ────────────────────────────────────────────────────────

func TestTemplateTestEmpty(t *testing.T) {
	m := &Ops{}
	if _, err := m.TemplateTest(TemplateTestInput{}); err == nil {
		t.Fatalf("expected error for empty template")
	}
}

func TestTemplateTestSimpleRender(t *testing.T) {
	m := &Ops{}
	res, err := m.TemplateTest(TemplateTestInput{
		Template: "{{.Env.NAME}}",
		Context:  `{"Env":{"NAME":"world"}}`,
	})
	if err != nil {
		t.Fatalf("TemplateTest: %v", err)
	}
	if !res.OK || res.Rendered != "world" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

// TestTemplateTestExpressionsBatch covers the per-expression breakdown:
// one round-trip evaluates each expression against the same context. The
// FE used to fire N parallel calls and trip the rate limiter (every row
// came back 429 → "nil/error" while the combined render succeeded).
func TestTemplateTestExpressionsBatch(t *testing.T) {
	m := &Ops{}
	ctx := `{"Event":{"type":"webhook","payload":{"body":{"repository":{"full_name":"yogasw/wick"},"pull_request":{"number":716}}}}}`
	res, err := m.TemplateTest(TemplateTestInput{
		Template: "https://api.github.com/repos/{{.Event.Payload.body.repository.full_name}}/pulls/{{.Event.Payload.body.pull_request.number}}",
		Context:  ctx,
		Expressions: []string{
			"{{.Event.Payload.body.repository.full_name}}",
			"{{.Event.Payload.body.pull_request.number}}",
			"{{.Event.Payload.body.missing.thing}}",
		},
	})
	if err != nil {
		t.Fatalf("TemplateTest: %v", err)
	}
	if !res.OK || res.Rendered != "https://api.github.com/repos/yogasw/wick/pulls/716" {
		t.Fatalf("combined render wrong: ok=%v rendered=%q", res.OK, res.Rendered)
	}
	if len(res.Results) != 3 {
		t.Fatalf("want 3 expression results, got %d", len(res.Results))
	}
	if !res.Results[0].OK || res.Results[0].Rendered != "yogasw/wick" {
		t.Fatalf("expr 0: %+v", res.Results[0])
	}
	if !res.Results[1].OK || res.Results[1].Rendered != "716" {
		t.Fatalf("expr 1: %+v", res.Results[1])
	}
	// missing.thing → .missing is nil, .thing on nil errors. The row must
	// carry that error WITHOUT failing the combined render above.
	if res.Results[2].OK {
		t.Fatalf("expr 2 should fail (nil .missing.thing): %+v", res.Results[2])
	}
}

func TestTemplateTestSampleEvent(t *testing.T) {
	m := &Ops{}
	res, err := m.TemplateTest(TemplateTestInput{
		Template:    `{{.Node.trigger.payload.text}}`,
		SampleEvent: "slack.message",
	})
	if err != nil {
		t.Fatalf("TemplateTest: %v", err)
	}
	if !res.OK {
		t.Fatalf("OK=false, err=%q", res.Error)
	}
	if !strings.Contains(res.Rendered, "staging deploy") {
		t.Fatalf("rendered = %q, want substring 'staging deploy'", res.Rendered)
	}
}

func TestTemplateTestMissingKeyHint(t *testing.T) {
	m := &Ops{}
	res, err := m.TemplateTest(TemplateTestInput{
		Template:    `{{.Node.trigger.payload.channel}}`,
		SampleEvent: "slack.message",
	})
	if err != nil {
		t.Fatalf("TemplateTest: %v", err)
	}
	// Engine now uses missingkey=zero (a webhook body without an optional
	// field must not fail the run), so a missing map key renders the zero
	// value ("<no value>" for map[string]any) instead of erroring. The
	// did-you-mean hint only fired on the old missingkey=error path, so it
	// no longer triggers here. Preview surfaces the empty value instead —
	// the per-expression table shows it as "(empty)".
	if !res.OK {
		t.Fatalf("missingkey=zero should render rather than fail: %+v", res)
	}
	if res.Rendered != "<no value>" {
		t.Fatalf("missing key on map renders <no value>, got %q", res.Rendered)
	}
}

func TestTemplateTestUnknownSampleEvent(t *testing.T) {
	m := &Ops{}
	if _, err := m.TemplateTest(TemplateTestInput{
		Template:    "x",
		SampleEvent: "nope",
	}); err == nil {
		t.Fatalf("expected error on unknown sample_event")
	}
}

// ── PickerResolve ───────────────────────────────────────────────────────

func TestPickerResolveNoRegistry(t *testing.T) {
	m := &Ops{}
	if _, err := m.PickerResolve(context.Background(), PickerResolveInput{Source: "x"}); err == nil {
		t.Fatalf("expected error when registry not configured")
	}
}

func TestPickerResolveEmptySource(t *testing.T) {
	m := &Ops{Pickers: NewPickerRegistry()}
	if _, err := m.PickerResolve(context.Background(), PickerResolveInput{}); err == nil {
		t.Fatalf("expected error on empty source")
	}
}

func TestPickerResolveUnknownSource(t *testing.T) {
	m := &Ops{Pickers: NewPickerRegistry()}
	m.Pickers.Register("slack.channels", func(_ context.Context, _ string) ([]PickerItem, error) {
		return nil, nil
	})
	_, err := m.PickerResolve(context.Background(), PickerResolveInput{Source: "slack.unknown"})
	if err == nil {
		t.Fatalf("expected error on unknown source")
	}
	if !strings.Contains(err.Error(), "available") {
		t.Fatalf("error should hint at available sources: %v", err)
	}
}

func TestPickerResolveQueryFilter(t *testing.T) {
	m := &Ops{Pickers: NewPickerRegistry()}
	m.Pickers.Register("slack.channels", func(_ context.Context, _ string) ([]PickerItem, error) {
		return []PickerItem{
			{ID: "C1", Name: "#general"},
			{ID: "C2", Name: "#support"},
			{ID: "C3", Name: "#dev"},
		}, nil
	})
	res, err := m.PickerResolve(context.Background(), PickerResolveInput{Source: "slack.channels", Query: "sup"})
	if err != nil {
		t.Fatalf("PickerResolve: %v", err)
	}
	if len(res.Items) != 1 || res.Items[0].ID != "C2" {
		t.Fatalf("unexpected items: %+v", res.Items)
	}
}

func TestPickerResolveLimit(t *testing.T) {
	m := &Ops{Pickers: NewPickerRegistry()}
	m.Pickers.Register("x", func(_ context.Context, _ string) ([]PickerItem, error) {
		return []PickerItem{{ID: "a"}, {ID: "b"}, {ID: "c"}}, nil
	})
	res, err := m.PickerResolve(context.Background(), PickerResolveInput{Source: "x", Limit: 2})
	if err != nil {
		t.Fatalf("PickerResolve: %v", err)
	}
	if len(res.Items) != 2 || !res.Truncated {
		t.Fatalf("expected 2 items + truncated=true: %+v", res)
	}
}

// ── Describe ────────────────────────────────────────────────────────────

func TestDescribeWorkflow(t *testing.T) {
	// Build a stub Ops with a fake service returning a known workflow.
	stub := newStubService(stubWorkflow())
	m := &Ops{Service: stub}
	res, err := m.Describe("wf1")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if res.Graph.NodeCount != 3 || res.Graph.EdgeCount != 2 {
		t.Fatalf("graph counts: %+v", res.Graph)
	}
	if len(res.Triggers) != 1 || res.Triggers[0].Type != "channel" {
		t.Fatalf("triggers: %+v", res.Triggers)
	}
	if len(res.Dependencies.Channels) == 0 || res.Dependencies.Channels[0] != "slack" {
		t.Fatalf("channels dep missing: %+v", res.Dependencies)
	}
	if len(res.Graph.Leaves) == 0 {
		t.Fatalf("expected at least one leaf node")
	}
}

func TestDescribeDanglingEdge(t *testing.T) {
	w := stubWorkflow()
	w.Graph.Edges = append(w.Graph.Edges, workflowEdge("classify", "missing"))
	stub := newStubService(w)
	m := &Ops{Service: stub}
	res, err := m.Describe("wf1")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	found := false
	for _, iss := range res.Issues {
		if strings.Contains(iss.Message, "missing") && iss.Level == "error" {
			found = true
		}
	}
	if !found {
		t.Fatalf("dangling edge issue missing: %+v", res.Issues)
	}
}

func TestDescribeTemplateRefWarning(t *testing.T) {
	w := stubWorkflow()
	// Override a node's url to reference an undeclared node.
	for i, n := range w.Graph.Nodes {
		if n.ID == "post" {
			w.Graph.Nodes[i].URL = "https://x/{{.Node.unknown_node.value}}"
		}
	}
	stub := newStubService(w)
	m := &Ops{Service: stub}
	res, err := m.Describe("wf1")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	found := false
	for _, iss := range res.Issues {
		if strings.Contains(iss.Message, "unknown_node") && iss.Level == "warning" {
			found = true
		}
	}
	if !found {
		t.Fatalf("template-ref warning missing: %+v", res.Issues)
	}
}

// ── Declarer flexibility ────────────────────────────────────────────────

// declarerExec stands in for a future custom node type (e.g. google_sheet).
// It implements both optional declarer interfaces so the describe layer can
// surface its dependency + custom templateable field without hardcoding.
type declarerExec struct{}

func (declarerExec) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	return workflow.NodeOutput{}, nil
}

func (declarerExec) Dependencies(n workflow.Node) []engine.NodeDependency {
	// Map a hypothetical n.Args["sheet"] into a sheet dependency.
	ref, _ := n.Args["sheet"].(string)
	if ref == "" {
		return nil
	}
	return []engine.NodeDependency{{Kind: engine.DepKindSheet, Ref: ref}}
}

func (declarerExec) TemplateableFields(n workflow.Node) map[string]string {
	out := map[string]string{}
	if v, ok := n.Args["range"].(string); ok {
		out["args.range"] = v
	}
	return out
}

func TestDescribeHonorsDeclarers(t *testing.T) {
	w := workflow.Workflow{
		ID:      "wf2",
		Name:    "sheets",
		Enabled: true,
		Triggers: []workflow.Trigger{
			{Type: workflow.TriggerManual, EntryNode: "lookup"},
		},
		Graph: workflow.Graph{
			Entry: "lookup",
			Nodes: []workflow.Node{
				{
					ID:   "lookup",
					Type: workflow.NodeType("google_sheet"),
					Args: map[string]any{
						"sheet": "abc123",
						"range": "{{.Node.missing_node.value}}",
					},
				},
			},
		},
	}
	eng := &engine.Engine{
		Executors:   map[workflow.NodeType]workflow.Executor{},
		Descriptors: map[workflow.NodeType]engine.NodeDescriptor{},
		Triggers:    engine.NewTriggerRegistry(),
	}
	eng.Register(workflow.NodeType("google_sheet"), declarerExec{})

	stub := newStubService(w)
	m := &Ops{Service: stub, Engine: eng}
	res, err := m.Describe("wf2")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if res.Dependencies.Other == nil || len(res.Dependencies.Other[engine.DepKindSheet]) != 1 {
		t.Fatalf("expected sheet dep under deps.other.sheet; got %+v", res.Dependencies)
	}
	if res.Dependencies.Other[engine.DepKindSheet][0] != "abc123" {
		t.Fatalf("sheet ref mismatch: %v", res.Dependencies.Other[engine.DepKindSheet])
	}
	found := false
	for _, iss := range res.Issues {
		if strings.Contains(iss.Message, "missing_node") && strings.Contains(iss.Path, "args.range") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected template-ref warning on args.range from declarer; issues=%+v", res.Issues)
	}
}
