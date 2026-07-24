package wick

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yogasw/wick/pkg/safeexec"
	"google.golang.org/genai"
)

// shellDefaultTimeout bounds a single shell invocation. Matches the
// conservative end of what the CLI agents get; overridable later via
// instance config.
const shellDefaultTimeout = 120 * time.Second

// shellMaxOutput caps combined stdout+stderr returned to the model so a
// runaway command (e.g. `yes`) can't blow the context budget.
const shellMaxOutput = 30_000

// shellTool runs a bash/cmd command in the session workspace. The gate
// is enforced upstream in the engine's before-tool check (gate.go), not
// here — this handler assumes the command was already allowed.
func shellTool(tc toolContext) toolDef {
	return toolDef{
		decl: &genai.FunctionDeclaration{
			Name: "shell",
			Description: "Run a shell command in the session workspace and return its combined " +
				"stdout+stderr. Use for file inspection, running builds/tests, and OS tasks. " +
				"Output is truncated if very long.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"command": {
						Type:        genai.TypeString,
						Description: "The shell command line to execute.",
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
			cctx, cancel := context.WithTimeout(ctx, shellDefaultTimeout)
			defer cancel()

			bin, err := resolveBash()
			if err != nil {
				return fmt.Sprintf("error: %v", err), true
			}
			// Single -c argument, exactly as received: no re-quoting, no
			// escaping " -> \", no \n -> space collapsing. This is what
			// makes heredocs, multi-line Python, and nested quotes work.
			cmd := safeexec.CommandContext(cctx, bin, "-c", cmdline)
			if tc.Workspace != "" {
				cmd.Dir = tc.Workspace
			}
			out, runErr := cmd.CombinedOutput()
			body := string(out)
			truncated := false
			if len(body) > shellMaxOutput {
				body = body[:shellMaxOutput]
				truncated = true
			}
			if cctx.Err() == context.DeadlineExceeded {
				return body + fmt.Sprintf("\n...(timed out after %s)", shellDefaultTimeout), true
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
