package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/connector"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/codemode"
	"github.com/yogasw/wick/internal/codemode/scriptpath"
	"github.com/yogasw/wick/internal/entity"
)

// RunCodeSchema reflects the inspector form. Code uses the textarea
// widget; the UI swaps it for an Ace editor (TypeScript / JavaScript).
type RunCodeSchema struct {
	Code       string `wick:"key=code;textarea;desc=TypeScript or JavaScript run by Code Mode. Call connectors with await wick.call('conn:<id>/<op>', params) and return the result. The JSON from the input field is the global input. Leave empty when script_path is set."`
	ScriptPath string `wick:"key=script_path;desc=Instead of code: a script file relative to the project folder named in workspace (no .. or absolute path, max 256 KB), read fresh on every run. Example: scripts/report.ts"`
	Workspace  string `wick:"key=workspace;desc=Project (workspace id) whose folder script_path is read from. Required with script_path, ignored for inline code."`
	Lang       string `wick:"key=lang;dropdown=ts|js;desc=Script language (default ts)."`
	Input      string `wick:"key=input;textarea;desc=Optional JSON handed to the script as input. Rendered as a template first, so {{.Node.<id>.<field>}} works, and must be valid JSON after rendering."`
	Timeout    string `wick:"key=timeout_sec;number;desc=Script timeout in seconds (default 30, max 120)."`
}

// RunCodeFn runs one Code Mode request with every wick.call executed as
// user. Injected from the server (mcp handlers.RunCodeAs) so the workflow
// packages never import the connectors service.
type RunCodeFn func(ctx context.Context, req codemode.Request, user *entity.User, tagIDs []string) (*codemode.Result, error)

// RunCodeExecutor runs a Code Mode script (JS/TS on the codemode_goja
// runner) as the workflow creator. Every wick.call goes through the
// normal connector checks for that user, so the node never reaches more
// than its creator can.
//
// The engine pre-renders every string field before Execute (honouring
// arg_modes), so Input arrives rendered and is not rendered again here —
// a second pass would evaluate {{…}} inside upstream values.
type RunCodeExecutor struct {
	Registry *connector.Registry
	Run      RunCodeFn
	// ProjectDir resolves a project id to its folder, for script_path.
	// nil fails every script_path node with a clear error.
	ProjectDir func(projectID string) (string, error)
}

// NewRunCodeExecutor wires the executor. A nil run fails every run_code
// node with a clear error instead of panicking.
func NewRunCodeExecutor(reg *connector.Registry, run RunCodeFn) *RunCodeExecutor {
	return &RunCodeExecutor{Registry: reg, Run: run}
}

// Descriptor exposes schema + docs for the MCP catalog.
func (e *RunCodeExecutor) Descriptor() engine.NodeDescriptor {
	return engine.NodeDescriptor{
		Category:    engine.CategoryAction,
		Label:       "Run Code",
		Badge:       "TS / JS",
		Description: "Run a TypeScript/JavaScript script (Code Mode) as the workflow creator. The script reads `input`, calls connectors with await wick.call(tool_id, params) and returns a JSON value.",
		WhenToUse:   "Several connector calls with logic between them (loops, filtering, joining results) in one step, instead of a chain of connector + transform nodes. Calls run with the creator's access only. Session connectors are not available (workflows have no session). Needs the codemode_goja service plugin.",
		Example:     "{\n  \"id\": \"open_issues\",\n  \"type\": \"run_code\",\n  \"lang\": \"ts\",\n  \"input\": \"{\\\"repo\\\": \\\"{{.Node.trigger.payload.repo}}\\\"}\",\n  \"code\": \"const r = await wick.call('conn:github/list_issues', { repo: input.repo, state: 'open' })\\nreturn { count: r.length }\",\n  \"timeout_sec\": 30\n}",
		Schema:      integration.StructSchema(RunCodeSchema{}),
		Output: map[string]string{
			"ok":          "bool — true when the script finished without throwing.",
			"result":      "any — the script's return value, parsed JSON (null when it returned nothing).",
			"logs":        "[]string — console output of the script.",
			"calls":       "[]object — each wick.call: tool_id, label, ok, ms, error.",
			"error":       "string — the script error when ok is false.",
			"duration_ms": "number — run time in milliseconds.",
		},
	}
}

// Execute resolves the creator, runs the script through the injected
// runner and maps the result into node fields. A failed script fails the
// node; the returned output still carries its calls and logs.
func (e *RunCodeExecutor) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	code, err := e.source(n)
	if err != nil {
		return workflow.NodeOutput{}, err
	}
	lang := strings.ToLower(strings.TrimSpace(n.Lang))
	if lang == "" {
		lang = "ts"
	}
	if lang != "ts" && lang != "js" {
		return workflow.NodeOutput{}, fmt.Errorf("run_code node %q: lang %q is not ts or js", n.ID, n.Lang)
	}
	var input json.RawMessage
	if s := strings.TrimSpace(n.Input); s != "" {
		if !json.Valid([]byte(s)) {
			return workflow.NodeOutput{}, fmt.Errorf("run_code node %q: input is not valid JSON after rendering: %s", n.ID, clipRunCode(s, 200))
		}
		input = json.RawMessage(s)
	}
	if e.Run == nil {
		return workflow.NodeOutput{}, errors.New("run_code: Code Mode is not wired on this server")
	}

	// Same identity rule as the connector node: headless runs act as the
	// workflow creator, and a workflow without one does not run at all.
	if e.Registry == nil {
		return workflow.NodeOutput{}, errors.New("run_code: no connector registry to resolve the workflow owner")
	}
	u, tagIDs, uerr := e.Registry.ResolveUser(ctx, rc.Workflow.CreatedBy)
	if uerr != nil {
		return workflow.NodeOutput{}, fmt.Errorf("resolve workflow owner: %w", uerr)
	}
	if u == nil {
		return workflow.NodeOutput{}, fmt.Errorf("run_code node %q: workflow has no owner to run as", n.ID)
	}

	res, err := e.Run(ctx, codemode.Request{
		Lang:    lang,
		Code:    code,
		Input:   input,
		Timeout: runCodeTimeout(n.TimeoutSec),
	}, u, tagIDs)
	if err != nil {
		if errors.Is(err, codemode.ErrNoRunner) && !strings.Contains(err.Error(), "codemode_goja") {
			err = fmt.Errorf("%w (install the codemode_goja service plugin)", err)
		}
		return workflow.NodeOutput{}, fmt.Errorf("run_code: %w", err)
	}
	if res == nil {
		return workflow.NodeOutput{}, errors.New("run_code: runner returned no result")
	}

	out := runCodeOutput(res)
	if !res.OK {
		msg := res.Error
		if msg == "" {
			msg = "script failed"
		}
		return out, fmt.Errorf("run_code: %s (%d calls)", msg, len(res.Calls))
	}
	return out, nil
}

// source is the script to run: inline code, or script_path read fresh from
// the folder of the project in n.Workspace with the watch code step's rules
// (scriptpath.Read).
func (e *RunCodeExecutor) source(n workflow.Node) (string, error) {
	rel := strings.TrimSpace(n.ScriptPath)
	hasCode := strings.TrimSpace(n.Code) != ""
	switch {
	case hasCode && rel != "":
		return "", fmt.Errorf("run_code node %q takes code or script_path, not both", n.ID)
	case hasCode:
		return n.Code, nil
	case rel == "":
		return "", fmt.Errorf("run_code node %q has no code", n.ID)
	}
	pid := strings.TrimSpace(n.Workspace)
	if pid == "" {
		return "", fmt.Errorf("run_code node %q: script_path needs workspace (the project whose folder holds the script)", n.ID)
	}
	if e.ProjectDir == nil {
		return "", errors.New("run_code: script_path is not wired on this server")
	}
	dir, err := e.ProjectDir(pid)
	if err != nil {
		return "", fmt.Errorf("run_code node %q: project %s: %w", n.ID, pid, err)
	}
	src, err := scriptpath.Read(dir, rel)
	if err != nil {
		return "", fmt.Errorf("run_code node %q: %w", n.ID, err)
	}
	return src, nil
}

// runCodeTimeout clamps timeout_sec to Code Mode's limits.
func runCodeTimeout(sec int) time.Duration {
	if sec <= 0 {
		return codemode.DefaultTimeout
	}
	d := time.Duration(sec) * time.Second
	if d > codemode.MaxTimeout {
		return codemode.MaxTimeout
	}
	return d
}

// runCodeOutput maps a Code Mode result into node fields. Calls become
// plain maps so templates can index them ({{(index .Node.x.calls 0).ok}}).
func runCodeOutput(res *codemode.Result) workflow.NodeOutput {
	var result any
	if len(res.Result) > 0 {
		if err := json.Unmarshal(res.Result, &result); err != nil {
			result = string(res.Result)
		}
	}
	logs := res.Logs
	if logs == nil {
		logs = []string{}
	}
	calls := make([]map[string]any, 0, len(res.Calls))
	for _, c := range res.Calls {
		calls = append(calls, map[string]any{
			"tool_id": c.ToolID,
			"label":   c.Label,
			"ok":      c.OK,
			"ms":      c.MS,
			"error":   c.Error,
		})
	}
	return workflow.NodeOutput{
		Result: result,
		Fields: map[string]any{
			"ok":          res.OK,
			"result":      result,
			"logs":        logs,
			"calls":       calls,
			"error":       res.Error,
			"duration_ms": res.DurationMS,
		},
	}
}

// clipRunCode shortens s for an error message.
func clipRunCode(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
