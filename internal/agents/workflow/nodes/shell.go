// Package nodes contains the concrete Executor impls for every Node
// type. Each constructor (NewXExecutor) returns a workflow.Executor
// the engine registers via Engine.Register.
package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/engine"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/agents/workflow/template"
)

type shellSchema struct {
	Command     string `wick:"required;key=command;desc=Shell command list (YAML sequence) or string, rendered as template"`
	ShellEnv    string `wick:"key=env;desc=YAML map of environment variables"`
	Cwd         string `wick:"key=cwd;desc=Working directory"`
	ParseOutput string `wick:"key=parse_output;dropdown=raw|json|lines;desc=How to parse stdout"`
	Timeout     string `wick:"key=timeout;desc=Execution timeout e.g. 30s"`
}

func (e *ShellExecutor) Descriptor() engine.NodeDescriptor {
	return engine.NodeDescriptor{
		Description: "Execute a local shell command. Captures stdout/stderr/exit_code.",
		WhenToUse:   "Operating on local files or running a CLI tool.",
		Schema:      integration.StructSchema(shellSchema{}),
		Output: map[string]string{
			"stdout":    "string",
			"stderr":    "string",
			"exit_code": "int",
		},
	}
}

// ShellExecutor runs a process and captures stdout/stderr/exit_code.
// parse_output: raw (default) | json | lines.
type ShellExecutor struct{}

// NewShellExecutor constructs the shell executor.
func NewShellExecutor() *ShellExecutor { return &ShellExecutor{} }

// Execute runs the shell command described by node n.
func (e *ShellExecutor) Execute(ctx context.Context, n workflow.Node, rc *workflow.RunContext) (workflow.NodeOutput, error) {
	rctx := rc.RenderCtx()
	if len(n.Command) == 0 {
		return workflow.NodeOutput{}, fmt.Errorf("shell node %q has no command", n.ID)
	}
	args := make([]string, len(n.Command))
	for i, a := range n.Command {
		s, err := template.Render(a, rctx)
		if err != nil {
			return workflow.NodeOutput{}, fmt.Errorf("command[%d]: %w", i, err)
		}
		args[i] = s
	}
	timeout := time.Duration(n.TimeoutSec) * time.Second
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, args[0], args[1:]...)
	if len(n.ShellEnv) > 0 {
		envSlice := []string{}
		for k, v := range n.ShellEnv {
			rv, err := template.Render(v, rctx)
			if err != nil {
				return workflow.NodeOutput{}, fmt.Errorf("env %q: %w", k, err)
			}
			envSlice = append(envSlice, k+"="+rv)
		}
		cmd.Env = append(cmd.Env, envSlice...)
	}
	if n.Cwd != "" {
		cwd, err := template.Render(n.Cwd, rctx)
		if err != nil {
			return workflow.NodeOutput{}, err
		}
		cmd.Dir = cwd
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if ee, ok := err.(*exec.ExitError); ok {
		exitCode = ee.ExitCode()
		err = nil
	}
	if cctx.Err() == context.DeadlineExceeded {
		return workflow.NodeOutput{}, fmt.Errorf("shell timeout after %s", timeout)
	}
	if err != nil {
		return workflow.NodeOutput{}, fmt.Errorf("shell exec: %w", err)
	}

	out := workflow.NodeOutput{
		Fields: map[string]any{
			"stdout":    stdout.String(),
			"stderr":    stderr.String(),
			"exit_code": exitCode,
		},
	}
	switch n.ParseOutput {
	case "json":
		var v any
		if err := json.Unmarshal(stdout.Bytes(), &v); err != nil {
			return workflow.NodeOutput{}, fmt.Errorf("parse_output json: %w", err)
		}
		out.Fields["parsed"] = v
	case "lines":
		lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
		out.Fields["parsed"] = lines
	}
	return out, nil
}
