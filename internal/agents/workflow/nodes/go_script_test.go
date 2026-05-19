package nodes

import (
	"context"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/workflow"
)

func runGoScript(t *testing.T, n workflow.Node) workflow.NodeOutput {
	t.Helper()
	rc := &workflow.RunContext{
		Workflow:    workflow.Workflow{ID: "wf-test"},
		Event:       workflow.Event{Type: "manual", Payload: map[string]any{"x": float64(10), "name": "world"}},
		EnvValues:   map[string]string{"api_url": "https://abc.com"},
		Secrets:     map[string]string{},
		NodeOutputs: map[string]workflow.NodeOutput{},
		RunID:       "run-1",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := NewGoScriptExecutor().Execute(ctx, n, rc)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	return out
}

func TestGoScript_SimpleStdoutNumber(t *testing.T) {
	out := runGoScript(t, workflow.Node{
		ID:   "n1",
		Type: workflow.NodeGoScript,
		Code: `package main
import "fmt"
func main() { fmt.Println(42) }
`,
	})
	if got := out.Result; got != float64(42) {
		t.Fatalf("want 42, got %v (%T)", got, got)
	}
}

func TestGoScript_StdinCtxAccess(t *testing.T) {
	out := runGoScript(t, workflow.Node{
		ID:   "n2",
		Type: workflow.NodeGoScript,
		Code: `package main
import (
	"encoding/json"
	"os"
)
func main() {
	var c map[string]any
	json.NewDecoder(os.Stdin).Decode(&c)
	ev := c["Event"].(map[string]any)
	pl := ev["Payload"].(map[string]any)
	json.NewEncoder(os.Stdout).Encode(map[string]any{
		"greeting":   "hello " + pl["name"].(string),
		"x_plus_one": pl["x"].(float64) + 1,
	})
}
`,
	})
	fields := out.Fields
	if fields["greeting"] != "hello world" {
		t.Fatalf("want greeting=hello world, got %v", fields["greeting"])
	}
	if fields["x_plus_one"] != float64(11) {
		t.Fatalf("want x_plus_one=11, got %v", fields["x_plus_one"])
	}
	obj, ok := out.Result.(map[string]any)
	if !ok {
		t.Fatalf("want result to be object, got %T", out.Result)
	}
	if obj["greeting"] != "hello world" {
		t.Fatalf("result.greeting mismatch: %v", obj["greeting"])
	}
}

func TestGoScript_StderrCaptured(t *testing.T) {
	out := runGoScript(t, workflow.Node{
		ID:   "n3",
		Type: workflow.NodeGoScript,
		Code: `package main
import (
	"fmt"
	"os"
)
func main() {
	fmt.Fprintln(os.Stderr, "debug log")
	fmt.Print("\"done\"")
}
`,
	})
	if out.Result != "done" {
		t.Fatalf("want done, got %v", out.Result)
	}
	if got := out.Fields["stderr"].(string); got != "debug log\n" {
		t.Fatalf("want stderr=debug log, got %q", got)
	}
}

func TestGoScript_EmptyStdoutIsNilResult(t *testing.T) {
	out := runGoScript(t, workflow.Node{
		ID:   "n4",
		Type: workflow.NodeGoScript,
		Code: `package main
func main() {}
`,
	})
	if out.Result != nil {
		t.Fatalf("want nil result, got %v", out.Result)
	}
}

func TestGoScript_BadJSONIsError(t *testing.T) {
	rc := &workflow.RunContext{
		Workflow:    workflow.Workflow{},
		Event:       workflow.Event{},
		NodeOutputs: map[string]workflow.NodeOutput{},
	}
	_, err := NewGoScriptExecutor().Execute(context.Background(), workflow.Node{
		ID:   "n5",
		Type: workflow.NodeGoScript,
		Code: `package main
import "fmt"
func main() { fmt.Print("not json{{") }
`,
	}, rc)
	if err == nil {
		t.Fatal("want error on bad JSON stdout")
	}
}

func TestGoScript_EmptyCodeRejected(t *testing.T) {
	rc := &workflow.RunContext{Workflow: workflow.Workflow{}, NodeOutputs: map[string]workflow.NodeOutput{}}
	_, err := NewGoScriptExecutor().Execute(context.Background(), workflow.Node{ID: "x", Type: workflow.NodeGoScript}, rc)
	if err == nil {
		t.Fatal("want error on empty code")
	}
}
