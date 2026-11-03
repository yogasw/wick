package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/codemode"
)

// script_path is read fresh from the workspace project's folder on every run,
// with the same confinement as the watch code step.
func TestRunCode_ScriptPath(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "scripts", "report.ts")
	if err := os.WriteFile(file, []byte("return 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := &fakeRun{res: &codemode.Result{OK: true, Result: json.RawMessage(`1`)}}
	ex := NewRunCodeExecutor(runCodeRegistry(), f.fn)
	ex.ProjectDir = func(id string) (string, error) {
		if id != "proj-1" {
			return "", errors.New("project not found")
		}
		return dir, nil
	}
	rc := runCodeRC("owner-7")
	n := workflow.Node{ID: "rc", Type: workflow.NodeRunCode, ScriptPath: "scripts/report.ts", Workspace: "proj-1"}

	if _, err := ex.Execute(context.Background(), n, rc); err != nil || f.req.Code != "return 1" {
		t.Fatalf("run: err=%v code=%q", err, f.req.Code)
	}
	if err := os.WriteFile(file, []byte("return 2"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ex.Execute(context.Background(), n, rc); err != nil || f.req.Code != "return 2" {
		t.Fatalf("not re-read: err=%v code=%q", err, f.req.Code)
	}

	calls := f.called
	cases := []struct {
		name string
		n    workflow.Node
		want string
	}{
		{"escape", workflow.Node{ID: "rc", ScriptPath: "../x.ts", Workspace: "proj-1"}, "no .."},
		{"missing", workflow.Node{ID: "rc", ScriptPath: "scripts/gone.ts", Workspace: "proj-1"}, "not found"},
		{"no workspace", workflow.Node{ID: "rc", ScriptPath: "scripts/report.ts"}, "needs workspace"},
		{"unknown project", workflow.Node{ID: "rc", ScriptPath: "scripts/report.ts", Workspace: "other"}, "project not found"},
		{"both", workflow.Node{ID: "rc", Code: "return 3", ScriptPath: "scripts/report.ts", Workspace: "proj-1"}, "not both"},
	}
	for _, c := range cases {
		if _, err := ex.Execute(context.Background(), c.n, rc); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err=%v, want %q", c.name, err, c.want)
		}
	}
	if f.called != calls {
		t.Fatalf("a refused script_path must not reach the runner (%d runs)", f.called-calls)
	}

	ex.ProjectDir = nil
	if _, err := ex.Execute(context.Background(), n, rc); err == nil || !strings.Contains(err.Error(), "not wired") {
		t.Fatalf("unwired: %v", err)
	}
}
