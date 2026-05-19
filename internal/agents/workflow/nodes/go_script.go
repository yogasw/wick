package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/agents/workflow/template"
)

// yaegi reads YAEGI_SPECIAL_STDIO at interp.New() time to decide
// whether to honor Options.Stdin/Stdout/Stderr for the script's
// os.Stdin/Stdout/Stderr. We set it process-wide once at first use —
// the flag has no effect on production wick code (we don't rely on
// special stdio anywhere else), so flipping it on permanently is
// safe.
var yaegiStdioInit sync.Once

func ensureYaegiStdio() {
	yaegiStdioInit.Do(func() {
		_ = os.Setenv("YAEGI_SPECIAL_STDIO", "1")
	})
}

// GoScriptSchema reflects the inspector form. The Code field uses the
// textarea widget; UI swaps it for an Ace editor at hydrate time.
type GoScriptSchema struct {
	Code    string `wick:"required;key=code;textarea;desc=Full Go program. Read RunContext via json.NewDecoder(os.Stdin).Decode(&ctx); write result via json.NewEncoder(os.Stdout).Encode(v). Anything on stderr surfaces as 'stderr' field. Imports allowed (yaegi stdlib)."`
	Timeout string `wick:"key=timeout_sec;number;desc=Script timeout in seconds (default 10)."`
}

// GoScriptExecutor runs a Go program inside the yaegi interpreter and
// pipes RenderCtx JSON to its stdin / parses JSON back from its
// stdout. No external toolchain required — yaegi ships with stdlib
// symbols (go1.21 surface), so the script can import anything the
// stdlib bundle covers (encoding/json, strings, time, regexp, math,
// net/url, …).
//
// Contract:
//
//	Input  : os.Stdin = JSON-encoded RenderCtx
//	         (keys: Event, Node, Env, Secret, Workflow, Run, Dataset)
//	Output : os.Stdout = JSON value of any shape; engine parses it and
//	         exposes as Node.<id>.result + merges top-level keys when
//	         the value is a JSON object so {{.Node.<id>.foo}} works.
//	         os.Stderr = free-form logs; surfaced as
//	         Node.<id>.stderr without further parsing.
//
// Template pre-rendering: Code itself is rendered through the wick
// template engine before yaegi sees it. Lets users splice values like
// `{{.Env.api_url}}` directly into source if they want — but the
// idiomatic path is to read from os.Stdin.
type GoScriptExecutor struct{}

// NewGoScriptExecutor wires the executor.
func NewGoScriptExecutor() *GoScriptExecutor { return &GoScriptExecutor{} }

// Descriptor exposes schema + docs for the MCP catalog.
func (e *GoScriptExecutor) Descriptor() engine.NodeDescriptor {
	return engine.NodeDescriptor{
		Description: "Run a Go program (yaegi interpreter). Stdin = run context JSON, stdout = result JSON.",
		WhenToUse:   "Logic that needs real Go code — string manipulation, math, JSON shaping, custom predicates. Use http/transform for I/O; this node is pure compute.",
		Example:     "- id: shape_payload\n  type: go_script\n  code: |\n    package main\n    import (\"encoding/json\"; \"os\")\n    func main() {\n      var ctx map[string]any\n      json.NewDecoder(os.Stdin).Decode(&ctx)\n      ev := ctx[\"Event\"].(map[string]any)[\"Payload\"].(map[string]any)\n      json.NewEncoder(os.Stdout).Encode(map[string]any{\"upper\": ev[\"text\"]})\n    }\n",
		Schema:      integration.StructSchema(GoScriptSchema{}),
		Output: map[string]string{
			"result": "any — JSON value parsed from script stdout. When result is an object, its keys are merged into Node.<id>.* for direct template access.",
			"stderr": "string — captured stderr, useful for debugging.",
		},
	}
}

// Execute renders code template, runs it under yaegi w/ stdin/stdout
// piped, and parses the stdout as JSON.
func (e *GoScriptExecutor) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	if strings.TrimSpace(n.Code) == "" {
		return workflow.NodeOutput{}, fmt.Errorf("go_script node %q has no code", n.ID)
	}

	rctx := rc.RenderCtx()
	src, err := template.Render(n.Code, rctx)
	if err != nil {
		return workflow.NodeOutput{}, fmt.Errorf("render code: %w", err)
	}

	ctxJSON, err := marshalRenderCtx(rctx)
	if err != nil {
		return workflow.NodeOutput{}, fmt.Errorf("marshal ctx: %w", err)
	}

	timeout := time.Duration(n.TimeoutSec) * time.Second
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ensureYaegiStdio()
	var stdout, stderr bytes.Buffer
	i := interp.New(interp.Options{
		Stdin:  bytes.NewReader(ctxJSON),
		Stdout: &stdout,
		Stderr: &stderr,
	})
	if err := i.Use(stdlib.Symbols); err != nil {
		return workflow.NodeOutput{}, fmt.Errorf("yaegi stdlib: %w", err)
	}

	if _, err := i.EvalWithContext(cctx, src); err != nil {
		if cctx.Err() == context.DeadlineExceeded {
			return workflow.NodeOutput{}, fmt.Errorf("go_script timeout after %s\nstderr: %s", timeout, stderr.String())
		}
		return workflow.NodeOutput{}, fmt.Errorf("go_script: %w\nstderr: %s", err, stderr.String())
	}

	result, err := parseScriptResult(stdout.Bytes())
	if err != nil {
		return workflow.NodeOutput{}, fmt.Errorf("parse stdout as JSON: %w\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}

	fields := map[string]any{
		"result": result,
		"stderr": stderr.String(),
	}
	// Convenience: when the script returns a JSON object, hoist its
	// top-level keys so downstream templates can reach them as
	// {{.Node.<id>.<key>}} without typing `.result.<key>`. Matches the
	// http/channel-action flattenFields pattern.
	if obj, ok := result.(map[string]any); ok {
		for k, v := range obj {
			if _, taken := fields[k]; taken {
				continue
			}
			fields[k] = v
		}
	}

	return workflow.NodeOutput{Result: result, Fields: fields}, nil
}

// parseScriptResult parses stdout as JSON. Empty stdout = nil result
// (scripts that intentionally produce no value are legal — they still
// expose stderr + run successfully).
func parseScriptResult(b []byte) (any, error) {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 {
		return nil, nil
	}
	var v any
	if err := json.Unmarshal(trimmed, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// marshalRenderCtx flattens RenderCtx into a stable JSON shape the
// script can decode. Mirrors the namespace user templates see —
// Event/Node/Env/Secret/Workflow/Run/Dataset as top-level keys.
func marshalRenderCtx(rctx workflow.RenderCtx) ([]byte, error) {
	payload := map[string]any{
		"Event": map[string]any{
			"Type":    rctx.Event.Type,
			"Subtype": rctx.Event.Subtype,
			"Channel": rctx.Event.Channel,
			"At":      rctx.Event.At,
			"Payload": rctx.Event.Payload,
		},
		"Node":     rctx.Node,
		"Env":      rctx.Env,
		"Secret":   rctx.Secret,
		"Workflow": map[string]any{"ID": rctx.Workflow.ID, "Version": rctx.Workflow.Version, "Name": rctx.Workflow.Name},
		"Run":      map[string]any{"ID": rctx.Run.ID, "StartedAt": rctx.Run.StartedAt},
		"Dataset":  rctx.Dataset,
	}
	return json.Marshal(payload)
}
