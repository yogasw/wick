package wick

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync/atomic"

	"github.com/yogasw/wick/pkg/proctree"
	"github.com/yogasw/wick/pkg/safeexec"
	"google.golang.org/genai"
)

// job_tools.go exposes the async job model (job.go) to the engine:
// job_start / job_status / job_log / job_cancel. job_start currently
// supports the "shell" runner; the runner switch is the extension point
// for wick_execute / playwright jobs later (see plan §7 Layer 3).

// shellJobRunner returns a jobRunner that runs one bash -c command to
// completion, streaming its combined output into the job buffer. It kills
// the whole process tree on ctx cancel (job_cancel / teardown) — same
// proctree machinery the foreground and background shells use. Unlike the
// foreground shell there is NO wall-clock deadline: a job runs until it
// finishes or is cancelled (that's the point of a job).
func shellJobRunner(workspace, cmdline string) jobRunner {
	return func(ctx context.Context, out io.Writer) (int, error) {
		bin, err := resolveBash()
		if err != nil {
			return -1, err
		}
		cmd := safeexec.Command(bin, "-c", cmdline)
		if workspace != "" {
			cmd.Dir = workspace
		}
		cmd.Stdout = out
		cmd.Stderr = out

		proctree.Apply(cmd)
		if err := cmd.Start(); err != nil {
			return -1, err
		}
		proctree.Assign(cmd)
		defer proctree.Release(cmd)

		var cancelled atomic.Bool
		done := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				cancelled.Store(true)
				_ = proctree.Kill(cmd)
			case <-done:
			}
		}()

		waitErr := cmd.Wait()
		close(done)

		if cancelled.Load() {
			return -1, context.Canceled
		}
		if waitErr != nil {
			if ec, ok := waitErr.(interface{ ExitCode() int }); ok {
				return ec.ExitCode(), nil
			}
			return -1, waitErr
		}
		return 0, nil
	}
}

// jobStartTool launches an async job. Returns a job_id at once; the job
// runs off-turn and, on completion, injects a [job-done …] turn so the
// agent is re-woken without polling.
func jobStartTool(tc toolContext) toolDef {
	return toolDef{
		decl: &genai.FunctionDeclaration{
			Name: "job_start",
			Description: "Start a long-running job that runs off-turn and NOTIFIES you when it " +
				"finishes — a [job-done …] message is delivered to the session automatically, so " +
				"you can start the job, answer the user, and be re-woken on completion instead of " +
				"polling. Use this for work that takes many minutes (installs, long scrapes, " +
				"batch runs) where you want the result pushed back. Returns a job_id immediately. " +
				"Check progress any time with job_status / job_log; stop it with job_cancel.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"tool": {
						Type:        genai.TypeString,
						Description: "Runner to use. Currently only \"shell\".",
						Enum:        []string{"shell"},
					},
					"command": {
						Type:        genai.TypeString,
						Description: "For tool=shell: the shell command line to run to completion.",
					},
				},
				Required: []string{"tool"},
			},
		},
		handler: func(ctx context.Context, args map[string]any) (string, bool) {
			if tc.Jobs == nil {
				return "error: jobs are not available in this session", true
			}
			tool, _ := args["tool"].(string)
			tool = strings.TrimSpace(tool)
			switch tool {
			case "shell", "":
				cmdline, _ := args["command"].(string)
				if strings.TrimSpace(cmdline) == "" {
					return "error: command is required for tool=shell", true
				}
				id, err := tc.Jobs.start(ctx, "shell", cmdline, shellJobRunner(tc.Workspace, cmdline))
				if err != nil {
					return fmt.Sprintf("error: %v", err), true
				}
				return fmt.Sprintf(
					"started job %s (shell) — you'll get a [job-done id=%s] message when it finishes; "+
						"check progress with job_status(job_id=%q) or job_log(job_id=%q), cancel with job_cancel(job_id=%q)",
					id, id, id, id, id), false
			default:
				return fmt.Sprintf("error: unknown job tool %q (only \"shell\" is supported)", tool), true
			}
		},
	}
}

// jobStatusTool reports a job's status + exit code (no full output).
func jobStatusTool(tc toolContext) toolDef {
	return toolDef{
		decl: &genai.FunctionDeclaration{
			Name: "job_status",
			Description: "Report a job's status (running / succeeded / failed / cancelled) and " +
				"exit_code once it ends. For the full output use job_log.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"job_id": {Type: genai.TypeString, Description: "The job_id returned by job_start."},
				},
				Required: []string{"job_id"},
			},
		},
		handler: func(_ context.Context, args map[string]any) (string, bool) {
			je, ok, errStr := lookupJob(tc, args)
			if !ok {
				return errStr, true
			}
			status, _, _, exitCode, errMsg := je.snapshot()
			var sb strings.Builder
			fmt.Fprintf(&sb, "status: %s\n", status)
			if status != jobRunning {
				fmt.Fprintf(&sb, "exit_code: %d\n", exitCode)
				if errMsg != "" {
					fmt.Fprintf(&sb, "error: %s\n", errMsg)
				}
			}
			return strings.TrimRight(sb.String(), "\n"), false
		},
	}
}

// jobLogTool returns a job's captured output (tail if very long).
func jobLogTool(tc toolContext) toolDef {
	return toolDef{
		decl: &genai.FunctionDeclaration{
			Name:        "job_log",
			Description: "Return a job's captured combined stdout+stderr (the tail only if very long).",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"job_id": {Type: genai.TypeString, Description: "The job_id returned by job_start."},
				},
				Required: []string{"job_id"},
			},
		},
		handler: func(_ context.Context, args map[string]any) (string, bool) {
			je, ok, errStr := lookupJob(tc, args)
			if !ok {
				return errStr, true
			}
			status, out, truncated, _, _ := je.snapshot()
			var sb strings.Builder
			fmt.Fprintf(&sb, "status: %s\n--- output ---\n", status)
			if truncated {
				sb.WriteString("...(earlier output truncated)\n")
			}
			if strings.TrimSpace(out) == "" {
				sb.WriteString("(no output yet)")
			} else {
				sb.WriteString(out)
			}
			return sb.String(), false
		},
	}
}

// jobCancelTool stops a running job (and its process tree).
func jobCancelTool(tc toolContext) toolDef {
	return toolDef{
		decl: &genai.FunctionDeclaration{
			Name:        "job_cancel",
			Description: "Stop a running job, terminating its work (and its whole process tree for shell jobs). Idempotent.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"job_id": {Type: genai.TypeString, Description: "The job_id returned by job_start."},
				},
				Required: []string{"job_id"},
			},
		},
		handler: func(_ context.Context, args map[string]any) (string, bool) {
			if tc.Jobs == nil {
				return "error: jobs are not available in this session", true
			}
			id, _ := args["job_id"].(string)
			id = strings.TrimSpace(id)
			if id == "" {
				return "error: job_id is required", true
			}
			if err := tc.Jobs.cancel(id); err != nil {
				return fmt.Sprintf("error: %v", err), true
			}
			return fmt.Sprintf("cancelled job %s", id), false
		},
	}
}

// lookupJob resolves the job_id arg to an entry, returning a ready-made
// error string on any miss.
func lookupJob(tc toolContext, args map[string]any) (*jobEntry, bool, string) {
	if tc.Jobs == nil {
		return nil, false, "error: jobs are not available in this session"
	}
	id, _ := args["job_id"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, false, "error: job_id is required"
	}
	je, ok := tc.Jobs.get(id)
	if !ok {
		return nil, false, fmt.Sprintf("error: no job with job_id %q", id)
	}
	return je, true, ""
}
