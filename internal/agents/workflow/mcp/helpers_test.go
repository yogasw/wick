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
func (s *stubService) List() ([]string, error)                                { return []string{s.wf.ID}, nil }
func (s *stubService) Create(string, workflow.Workflow, map[string][]byte) error { return nil }
func (s *stubService) Update(string, workflow.Workflow, map[string][]byte) error { return nil }
func (s *stubService) Delete(string) error                                    { return nil }
func (s *stubService) Toggle(string, bool) error                              { return nil }
func (s *stubService) FindByName(string, string) (string, error)              { return "", nil }
func (s *stubService) LoadDraft(string) (workflow.Workflow, error) {
	return workflow.Workflow{}, service.ErrNotFound
}
func (s *stubService) HasDraft(string) bool                            { return false }
func (s *stubService) SaveDraft(string, workflow.Workflow) error       { return nil }
func (s *stubService) Publish(string) (workflow.Workflow, error)       { return s.wf, nil }
func (s *stubService) DiscardDraft(string) error                       { return nil }
func (s *stubService) ListFiles(string) ([]string, error)              { return nil, nil }
func (s *stubService) ReadFile(string, string) ([]byte, error)         { return nil, fmt.Errorf("stub") }
func (s *stubService) WriteFile(string, string, []byte) error          { return nil }
func (s *stubService) DeleteFile(string, string) error                 { return nil }
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
	if res.OK {
		t.Fatalf("expected OK=false on missing key")
	}
	if len(res.AvailableKeys) == 0 {
		t.Fatalf("expected available_keys to be populated")
	}
	foundChannelID := false
	for _, k := range res.AvailableKeys {
		if k == "channel_id" {
			foundChannelID = true
		}
	}
	if !foundChannelID {
		t.Fatalf("available_keys should include channel_id: %v", res.AvailableKeys)
	}
	if res.Hint == "" {
		t.Fatalf("expected did-you-mean hint (channel → channel_id)")
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
