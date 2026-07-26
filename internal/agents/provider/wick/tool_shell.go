package wick

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/yogasw/wick/pkg/proctree"
	"github.com/yogasw/wick/pkg/safeexec"
	"google.golang.org/genai"
)

// shellDefaultTimeout bounds a single foreground shell invocation when
// the caller supplies no timeout_ms. 10m comfortably covers ordinary
// installs/builds (npm i, go build, test runs) so the model rarely needs
// to set a timeout at all. Work that legitimately runs longer — or that
// should proceed in parallel — belongs in a background shell
// (run_in_background), not a longer foreground deadline.
const shellDefaultTimeout = 10 * time.Minute

// shellMaxTimeout is the hard ceiling a caller-supplied timeout_ms is
// clamped to. The engine runs in-process (no SSE 5m outer cap applies —
// that ceiling only bounds external MCP-over-HTTP callers), and the pool
// idle-kill is paused while a tool is in flight (the engine emits the
// tool_use line before dispatch, so the reader marks toolInFlight), so
// the only thing bounding a foreground shell is this constant. 30m is a
// generous ceiling for a single blocking call; past it, use a background
// shell so the turn isn't held hostage.
const shellMaxTimeout = 30 * time.Minute

// shellMinTimeout floors a caller-supplied timeout_ms so a fat-fingered
// tiny value (e.g. 5) doesn't make every command instantly time out.
const shellMinTimeout = time.Second

// shellMaxOutput caps combined stdout+stderr returned to the model so a
// runaway command (e.g. `yes`) can't blow the context budget.
const shellMaxOutput = 30_000

// resolveShellTimeout picks the deadline for one shell call from the
// optional timeout_ms arg: absent/zero → default, otherwise clamped to
// [shellMinTimeout, shellMaxTimeout]. Accepts the JSON-decoded numeric
// shapes a model may emit (float64 from encoding/json, int, or a numeric
// string) so a valid intent isn't silently dropped to the default.
func resolveShellTimeout(args map[string]any) time.Duration {
	ms, ok := numericArg(args["timeout_ms"])
	if !ok || ms <= 0 {
		return shellDefaultTimeout
	}
	d := time.Duration(ms) * time.Millisecond
	if d < shellMinTimeout {
		return shellMinTimeout
	}
	if d > shellMaxTimeout {
		return shellMaxTimeout
	}
	return d
}

// numericArg coerces a JSON-decoded value to an int64, tolerating the
// float64 encoding/json produces, a native int, and a numeric string.
func numericArg(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case int:
		return int64(n), true
	case int64:
		return n, true
	case string:
		if i, err := strconv.ParseInt(strings.TrimSpace(n), 10, 64); err == nil {
			return i, true
		}
	}
	return 0, false
}

// shellTool runs a bash/cmd command in the session workspace. The gate
// is enforced upstream in the engine's before-tool check (gate.go), not
// here — this handler assumes the command was already allowed.
func shellTool(tc toolContext) toolDef {
	return toolDef{
		decl: &genai.FunctionDeclaration{
			Name: "shell",
			Description: "Run a shell command in the session workspace and return its combined " +
				"stdout+stderr. Use for file inspection, running builds/tests, and OS tasks. " +
				"Output is truncated if very long. Blocks until the command finishes or " +
				"times out (default 10m, override with timeout_ms up to 30m). For work that " +
				"runs longer than that, or that should proceed while you keep working, use a " +
				"background shell instead.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"command": {
						Type:        genai.TypeString,
						Description: "The shell command line to execute.",
					},
					"timeout_ms": {
						Type: genai.TypeInteger,
						Description: "Optional per-call deadline in milliseconds. Defaults to " +
							"600000 (10m); clamped to [1000, 1800000] (1s–30m). The command is " +
							"killed when it fires. Ignored when run_in_background is true.",
					},
					"run_in_background": {
						Type: genai.TypeBoolean,
						Description: "Run detached and return a bash_id immediately instead of " +
							"blocking. Use for long installs/builds, servers, or watchers you want " +
							"to keep working alongside. Poll it with shell_output(bash_id) and stop " +
							"it with shell_kill(bash_id).",
					},
				},
				Required: []string{"command"},
			},
		},
		handler: func(ctx context.Context, args map[string]any) (string, bool) {
			// NOTE: cmdline is NOT trimmed of internal structure — only
			// outer whitespace. Multi-line bodies (heredocs, multi-command
			// scripts) must survive byte-for-byte; trimming/joining lines
			// or escaping quotes here is exactly the bug this tool must
			// never regress into (see plan.md "Shell provider spec").
			cmdline, _ := args["command"].(string)
			if strings.TrimSpace(cmdline) == "" {
				return "error: empty command", true
			}

			// Background: spawn detached, return a bash_id at once. The
			// foreground timeout does not apply — the command lives until it
			// exits, is killed (shell_kill), or the session tears down.
			if bg, _ := args["run_in_background"].(bool); bg {
				if tc.Bg == nil {
					return "error: background shells are not available in this session", true
				}
				id, err := tc.Bg.start(cmdline)
				if err != nil {
					return fmt.Sprintf("error: %v", err), true
				}
				return fmt.Sprintf(
					"started background shell %s — poll it with shell_output(bash_id=%q), stop it with shell_kill(bash_id=%q)",
					id, id, id), false
			}

			timeout := resolveShellTimeout(args)
			out, timedOut, runErr := runShellForeground(ctx, tc.Workspace, cmdline, timeout)

			body := string(out)
			truncated := false
			if len(body) > shellMaxOutput {
				body = body[:shellMaxOutput]
				truncated = true
			}
			if timedOut {
				return body + fmt.Sprintf(
					"\n...(timed out after %s — raise timeout_ms (max %s) or run it in the background)",
					timeout, shellMaxTimeout), true
			}
			isErr := runErr != nil
			if truncated {
				body += "\n...(truncated)"
			}
			if strings.TrimSpace(body) == "" {
				body = "(no output)"
			}
			if isErr {
				body += fmt.Sprintf("\n(exit error: %v)", runErr)
			}
			return body, isErr
		},
	}
}

// runShellForeground runs one bash -c command to completion, its output
// captured combined (stdout+stderr). It kills the WHOLE process tree — not
// just bash — when timeout elapses or ctx is cancelled, so a child like
// `sleep`/`node` can't keep running (and, on Windows, keep the output pipe
// open) past the deadline. Returns the captured bytes so far, whether the
// deadline fired, and the raw run error (nil on clean exit).
//
// This deliberately uses safeexec.Command + a manual watcher rather than
// CommandContext: exec's context kill targets only the direct child, which
// is exactly the bug (bash dies, its `sleep` child lives, CombinedOutput
// blocks on the still-open pipe). proctree.Kill reaps the group instead.
func runShellForeground(ctx context.Context, workspace, cmdline string, timeout time.Duration) (out []byte, timedOut bool, runErr error) {
	bin, err := resolveBash()
	if err != nil {
		return nil, false, fmt.Errorf("error: %w", err)
	}
	// Single -c argument, exactly as received: no re-quoting, no escaping
	// " -> \", no \n -> space collapsing. This is what makes heredocs,
	// multi-line Python, and nested quotes work.
	cmd := safeexec.Command(bin, "-c", cmdline)
	if workspace != "" {
		cmd.Dir = workspace
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	proctree.Apply(cmd)
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}
	proctree.Assign(cmd)
	defer proctree.Release(cmd)

	// Watch the deadline and ctx cancellation alongside the process. The
	// first trigger wins: on fire we kill the whole tree, which unblocks
	// cmd.Wait; on clean exit the done channel stops the watcher.
	var killed atomic.Bool
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	done := make(chan struct{})
	go func() {
		select {
		case <-deadline.C:
			killed.Store(true)
			_ = proctree.Kill(cmd)
		case <-ctx.Done():
			_ = proctree.Kill(cmd)
		case <-done:
		}
	}()

	waitErr := cmd.Wait()
	close(done)

	if killed.Load() {
		// Deadline fired: report as a timeout regardless of the wait error
		// (a killed process reports a non-nil, non-meaningful exit).
		return buf.Bytes(), true, nil
	}
	return buf.Bytes(), false, waitErr
}
