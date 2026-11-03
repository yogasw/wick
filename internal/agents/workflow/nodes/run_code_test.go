package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/connector"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/codemode"
	"github.com/yogasw/wick/internal/entity"
)

// fakeRun records what the executor hands to Code Mode and answers with res.
type fakeRun struct {
	req    codemode.Request
	user   *entity.User
	tags   []string
	called int
	res    *codemode.Result
	err    error
}

func (f *fakeRun) fn(ctx context.Context, req codemode.Request, u *entity.User, tags []string) (*codemode.Result, error) {
	f.called++
	f.req, f.user, f.tags = req, u, tags
	return f.res, f.err
}

func runCodeRegistry() *connector.Registry {
	reg := connector.NewRegistry(nil, nil)
	reg.SetUserResolver(ownerResolver("owner-7"))
	return reg
}

func runCodeRC(createdBy string) *workflow.RunContext {
	rc := connRC(createdBy)
	rc.NodeOutputs = map[string]workflow.NodeOutput{
		"fetch": {Fields: map[string]any{"name": "alpha", "count": 3}},
	}
	return rc
}

// The input template is rendered by the engine's pre-render, handed to
// the script as JSON, and the run happens as the workflow owner.
func TestRunCode_SuccessWithRenderedInput(t *testing.T) {
	f := &fakeRun{res: &codemode.Result{
		OK:         true,
		Result:     json.RawMessage(`{"total":4}`),
		Logs:       []string{"hello"},
		Calls:      []codemode.Call{{ToolID: "conn:c1/list", Label: "List", OK: true, MS: 12}},
		DurationMS: 40,
	}}
	rc := runCodeRC("owner-7")
	n := workflow.Node{ID: "rc", Type: workflow.NodeRunCode, Code: "return { total: input.count + 1 }",
		Input: `{"name":"{{.Node.fetch.name}}","count":{{.Node.fetch.count}}}`}
	n, err := engine.PreRenderNode(n, rc.RenderCtx())
	if err != nil {
		t.Fatalf("pre-render: %v", err)
	}

	out, err := NewRunCodeExecutor(runCodeRegistry(), f.fn).Execute(context.Background(), n, rc)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if got := string(f.req.Input); got != `{"name":"alpha","count":3}` {
		t.Errorf("input = %s", got)
	}
	if f.req.Lang != "ts" || f.req.Timeout != codemode.DefaultTimeout {
		t.Errorf("lang/timeout = %q/%s, want ts/%s", f.req.Lang, f.req.Timeout, codemode.DefaultTimeout)
	}
	if f.user == nil || f.user.ID != "owner-7" || len(f.tags) != 1 || f.tags[0] != "tag-a" {
		t.Errorf("ran as %+v tags %v, want owner-7 [tag-a]", f.user, f.tags)
	}
	if out.Fields["ok"] != true || out.Fields["duration_ms"] != int64(40) {
		t.Errorf("ok/duration = %v/%v", out.Fields["ok"], out.Fields["duration_ms"])
	}
	if r, _ := out.Fields["result"].(map[string]any); r["total"] != float64(4) {
		t.Errorf("result = %v", out.Fields["result"])
	}
	calls, _ := out.Fields["calls"].([]map[string]any)
	if len(calls) != 1 || calls[0]["tool_id"] != "conn:c1/list" || calls[0]["ok"] != true || calls[0]["ms"] != int64(12) {
		t.Errorf("calls = %v", out.Fields["calls"])
	}
	if logs, _ := out.Fields["logs"].([]string); len(logs) != 1 || logs[0] != "hello" {
		t.Errorf("logs = %v", out.Fields["logs"])
	}
}

// A script that throws fails the node, but the output keeps what it did.
func TestRunCode_ScriptFailureKeepsCallsAndLogs(t *testing.T) {
	f := &fakeRun{res: &codemode.Result{
		OK:    false,
		Error: "TypeError: x is undefined",
		Logs:  []string{"before"},
		Calls: []codemode.Call{{ToolID: "conn:c1/get", OK: false, MS: 5, Error: "not found"}},
	}}
	n := workflow.Node{ID: "rc", Type: workflow.NodeRunCode, Code: "throw new Error()", Lang: "js"}
	out, err := NewRunCodeExecutor(runCodeRegistry(), f.fn).Execute(context.Background(), n, runCodeRC("owner-7"))
	if err == nil || !strings.Contains(err.Error(), "TypeError: x is undefined") {
		t.Fatalf("err = %v, want the script error", err)
	}
	if f.req.Lang != "js" {
		t.Errorf("lang = %q, want js", f.req.Lang)
	}
	if out.Fields["ok"] != false || out.Fields["error"] != "TypeError: x is undefined" {
		t.Errorf("ok/error = %v/%v", out.Fields["ok"], out.Fields["error"])
	}
	calls, _ := out.Fields["calls"].([]map[string]any)
	if len(calls) != 1 || calls[0]["error"] != "not found" {
		t.Errorf("calls = %v", out.Fields["calls"])
	}
	if logs, _ := out.Fields["logs"].([]string); len(logs) != 1 {
		t.Errorf("logs = %v", out.Fields["logs"])
	}
}

func TestRunCode_InvalidInputJSON(t *testing.T) {
	f := &fakeRun{res: &codemode.Result{OK: true}}
	n := workflow.Node{ID: "rc", Type: workflow.NodeRunCode, Code: "return 1", Input: `{"name": alpha}`}
	_, err := NewRunCodeExecutor(runCodeRegistry(), f.fn).Execute(context.Background(), n, runCodeRC("owner-7"))
	if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("err = %v, want invalid JSON", err)
	}
	if f.called != 0 {
		t.Errorf("runner called %d times, want 0", f.called)
	}
}

// No creator → refuse; never fall back to some wider identity.
func TestRunCode_NoCreatorRefuses(t *testing.T) {
	for _, createdBy := range []string{"", "someone-else"} {
		f := &fakeRun{res: &codemode.Result{OK: true}}
		n := workflow.Node{ID: "rc", Type: workflow.NodeRunCode, Code: "return 1"}
		_, err := NewRunCodeExecutor(runCodeRegistry(), f.fn).Execute(context.Background(), n, runCodeRC(createdBy))
		if err == nil || !strings.Contains(err.Error(), "no owner") {
			t.Errorf("createdBy %q: err = %v, want no owner", createdBy, err)
		}
		if f.called != 0 {
			t.Errorf("createdBy %q: runner called", createdBy)
		}
	}
}

func TestRunCode_TimeoutClamped(t *testing.T) {
	cases := map[int]time.Duration{0: codemode.DefaultTimeout, 5: 5 * time.Second, 120: 120 * time.Second, 600: 120 * time.Second}
	for sec, want := range cases {
		f := &fakeRun{res: &codemode.Result{OK: true}}
		n := workflow.Node{ID: "rc", Type: workflow.NodeRunCode, Code: "return 1", TimeoutSec: sec}
		if _, err := NewRunCodeExecutor(runCodeRegistry(), f.fn).Execute(context.Background(), n, runCodeRC("owner-7")); err != nil {
			t.Fatalf("timeout %d: %v", sec, err)
		}
		if f.req.Timeout != want {
			t.Errorf("timeout_sec %d → %s, want %s", sec, f.req.Timeout, want)
		}
	}
}

func TestRunCode_NoRunnerNamesPlugin(t *testing.T) {
	f := &fakeRun{err: codemode.ErrNoRunner}
	n := workflow.Node{ID: "rc", Type: workflow.NodeRunCode, Code: "return 1"}
	_, err := NewRunCodeExecutor(runCodeRegistry(), f.fn).Execute(context.Background(), n, runCodeRC("owner-7"))
	if !errors.Is(err, codemode.ErrNoRunner) || !strings.Contains(err.Error(), "codemode_goja") {
		t.Fatalf("err = %v, want ErrNoRunner naming codemode_goja", err)
	}
}

func TestRunCode_RejectsBadLangAndUnwiredRunner(t *testing.T) {
	n := workflow.Node{ID: "rc", Type: workflow.NodeRunCode, Code: "return 1", Lang: "py"}
	if _, err := NewRunCodeExecutor(runCodeRegistry(), (&fakeRun{}).fn).Execute(context.Background(), n, runCodeRC("owner-7")); err == nil {
		t.Error("lang py accepted")
	}
	n.Lang = ""
	if _, err := NewRunCodeExecutor(runCodeRegistry(), nil).Execute(context.Background(), n, runCodeRC("owner-7")); err == nil || !strings.Contains(err.Error(), "not wired") {
		t.Errorf("nil runner: err = %v", err)
	}
}

func TestRunCode_DescriptorDocumentsOutputs(t *testing.T) {
	d := NewRunCodeExecutor(nil, nil).Descriptor()
	for _, k := range []string{"ok", "result", "logs", "calls", "error", "duration_ms"} {
		if d.Output[k] == "" {
			t.Errorf("output %q undocumented", k)
		}
	}
	if !strings.Contains(d.WhenToUse, "Session connectors are not available") {
		t.Error("descriptor must say session connectors are unavailable")
	}
}
