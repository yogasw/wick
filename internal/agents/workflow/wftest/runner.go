// Package wftest runs workflow test cases loaded from `__tests__/`
// fixtures. Each case = synthetic event + expected outputs +
// assertions. Used by `workflow_test` MCP op and CLI
// `wick workflow test <id>`.
package wftest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/service"
)

// Case is one fixture in `__tests__/`.
type Case struct {
	Name           string         `json:"name,omitempty"`
	Input          Input          `json:"input"`
	ExpectedOutput map[string]any `json:"expected_output,omitempty"`
	Assertions     []Assertion    `json:"assertions,omitempty"`
}

// Input is what gets passed to the engine as the trigger event.
type Input struct {
	Event workflow.Event `json:"Event"`
	Node  map[string]any `json:"Node,omitempty"`
}

// Assertion is one expectation evaluated against the run state.
type Assertion struct {
	Subject  string `json:"subject"`
	Operator string `json:"operator"`
	Value    any    `json:"value,omitempty"`
}

// Result is what Runner.RunAll returns per case.
type Result struct {
	Name       string
	Pass       bool
	Failures   []string
	NodeOutput map[string]any
	State      workflow.RunState
	Duration   time.Duration
}

// Coverage summarises which nodes were touched across all test runs.
type Coverage struct {
	// TotalNodes is the count of non-trigger nodes in the workflow graph.
	TotalNodes int
	// HitNodes is the set of node IDs that completed in at least one run.
	HitNodes map[string]bool
	// Untested is the list of node IDs that never ran.
	Untested []string
}

// HitCount returns the number of nodes hit.
func (c Coverage) HitCount() int { return len(c.HitNodes) }

// Percent returns coverage as 0–100.
func (c Coverage) Percent() int {
	if c.TotalNodes == 0 {
		return 100
	}
	return 100 * len(c.HitNodes) / c.TotalNodes
}

// Runner loads cases from `__tests__/` and runs them.
type Runner struct {
	Engine  *engine.Engine
	Service service.Service
	Layout  config.Layout
}

// New builds a runner.
func New(e *engine.Engine, svc service.Service, layout config.Layout) *Runner {
	return &Runner{Engine: e, Service: svc, Layout: layout}
}

// LoadCases reads every `__tests__/*.json` in a workflow folder.
func (r *Runner) LoadCases(id string) ([]Case, error) {
	dir := r.Layout.WorkflowTestsDir(id)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	cases := []Case{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		var tc Case
		if err := json.Unmarshal(data, &tc); err != nil {
			return nil, fmt.Errorf("decode %s: %w", e.Name(), err)
		}
		if tc.Name == "" {
			tc.Name = strings.TrimSuffix(e.Name(), ".json")
		}
		cases = append(cases, tc)
	}
	return cases, nil
}

// RunAll executes every test case and returns per-case results.
func (r *Runner) RunAll(ctx context.Context, id string) ([]Result, error) {
	results, _, err := r.RunAllWithCoverage(ctx, id)
	return results, err
}

// RunAllWithCoverage runs all cases and computes node coverage.
func (r *Runner) RunAllWithCoverage(ctx context.Context, id string) ([]Result, Coverage, error) {
	cases, err := r.LoadCases(id)
	if err != nil {
		return nil, Coverage{}, err
	}
	w, err := r.Service.Load(id)
	if err != nil {
		return nil, Coverage{}, err
	}

	// Build the set of all non-trigger nodes in the graph.
	allNodes := map[string]bool{}
	for _, n := range w.Graph.Nodes {
		allNodes[n.ID] = true
	}

	hitNodes := map[string]bool{}
	results := []Result{}
	for _, tc := range cases {
		res := r.runOne(ctx, w, tc)
		results = append(results, res)
		for id := range res.State.Outputs {
			hitNodes[id] = true
		}
		for _, id := range res.State.Completed {
			hitNodes[id] = true
		}
	}

	untested := []string{}
	for id := range allNodes {
		if !hitNodes[id] {
			untested = append(untested, id)
		}
	}

	cov := Coverage{
		TotalNodes: len(allNodes),
		HitNodes:   hitNodes,
		Untested:   untested,
	}
	return results, cov, nil
}

// RunOne runs a single test case against a loaded workflow. Used by the
// per-case "▶" button in the UI.
func (r *Runner) RunOne(ctx context.Context, w workflow.Workflow, tc Case) Result {
	return r.runOne(ctx, w, tc)
}

func (r *Runner) runOne(ctx context.Context, w workflow.Workflow, tc Case) Result {
	start := time.Now()
	res := Result{Name: tc.Name}
	st, err := r.Engine.Run(ctx, w, tc.Input.Event)
	res.State = st
	res.NodeOutput = st.Outputs
	res.Duration = time.Since(start)
	if err != nil {
		res.Failures = append(res.Failures, "engine error: "+err.Error())
	}
	for _, exp := range tc.Assertions {
		if msg := evalAssertion(exp, st); msg != "" {
			res.Failures = append(res.Failures, msg)
		}
	}
	for k, want := range tc.ExpectedOutput {
		got, ok := st.Outputs[k]
		if !ok {
			res.Failures = append(res.Failures, fmt.Sprintf("expected_output[%q]: missing in actual", k))
			continue
		}
		if !equalLoose(got, want) {
			res.Failures = append(res.Failures, fmt.Sprintf("expected_output[%q]: want %v, got %v", k, want, got))
		}
	}
	res.Pass = len(res.Failures) == 0
	return res
}

func evalAssertion(a Assertion, st workflow.RunState) string {
	switch a.Operator {
	case "==", "eq":
		got := resolveSubject(a.Subject, st)
		if !equalLoose(got, a.Value) {
			return fmt.Sprintf("%s == %v: got %v", a.Subject, a.Value, got)
		}
	case "!=", "ne":
		got := resolveSubject(a.Subject, st)
		if equalLoose(got, a.Value) {
			return fmt.Sprintf("%s != %v: got equal", a.Subject, a.Value)
		}
	case "contains":
		got := fmt.Sprintf("%v", resolveSubject(a.Subject, st))
		want := fmt.Sprintf("%v", a.Value)
		if !strings.Contains(got, want) {
			return fmt.Sprintf("%s contains %q: got %q", a.Subject, want, got)
		}
	case "case_fired":
		want := fmt.Sprintf("%v", a.Value)
		if !containsStr(st.Completed, want) {
			return fmt.Sprintf("case_fired: node %q not in completed", want)
		}
	case "node_skipped":
		want := fmt.Sprintf("%v", a.Value)
		if !containsStr(st.Skipped, want) {
			return fmt.Sprintf("node_skipped: %q not in skipped", want)
		}
	case "path_taken":
		want := stringSliceOf(a.Value)
		for _, id := range want {
			if !containsStr(st.Completed, id) {
				return fmt.Sprintf("path_taken: %q not in completed", id)
			}
		}
	case "edge_traversed":
		want := fmt.Sprintf("%v", a.Value)
		parts := strings.Split(want, "->")
		if len(parts) != 2 {
			return fmt.Sprintf("edge_traversed expects 'from->to'; got %q", want)
		}
		if !containsStr(st.Completed, parts[0]) || !containsStr(st.Completed, parts[1]) {
			return fmt.Sprintf("edge_traversed %s: one endpoint not completed", want)
		}
	default:
		return fmt.Sprintf("unsupported assertion operator %q", a.Operator)
	}
	return ""
}

func resolveSubject(subject string, st workflow.RunState) any {
	if subject == "status" {
		return st.Status
	}
	if strings.HasPrefix(subject, "node.") {
		rest := strings.TrimPrefix(subject, "node.")
		parts := strings.SplitN(rest, ".", 2)
		nid := parts[0]
		out, ok := st.Outputs[nid]
		if !ok {
			return nil
		}
		if len(parts) == 1 {
			return out
		}
		m, ok := out.(map[string]any)
		if !ok {
			return nil
		}
		return m[parts[1]]
	}
	return nil
}

func equalLoose(a, b any) bool {
	return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b)
}

func stringSliceOf(v any) []string {
	if s, ok := v.([]any); ok {
		out := make([]string, 0, len(s))
		for _, x := range s {
			out = append(out, fmt.Sprintf("%v", x))
		}
		return out
	}
	if s, ok := v.([]string); ok {
		return s
	}
	return nil
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
