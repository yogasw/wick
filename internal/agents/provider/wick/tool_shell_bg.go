package wick

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/pkg/proctree"
	"github.com/yogasw/wick/pkg/safeexec"
	"google.golang.org/genai"
)

// tool_shell_bg.go adds background shells to the wick engine — the same
// affordance claude/codex expose as run_in_background + BashOutput +
// KillShell. A foreground `shell` blocks the turn until the command ends;
// a background shell returns a bash_id immediately, keeps running detached
// (its whole process tree tracked via proctree), and the model polls it
// with shell_output / stops it with shell_kill.
//
// Registry lifetime is the spawn: one *bgRegistry per engine, created in
// buildTools and reaped when the process is killed (see closeBg wiring in
// process.go). Output is ring-capped at shellMaxOutput so a chatty
// background command (`npm i`, a dev server) can't grow unbounded in
// memory.

// bgStatus is the lifecycle state of a background shell.
type bgStatus string

const (
	bgRunning bgStatus = "running"
	bgExited  bgStatus = "exited"
	bgKilled  bgStatus = "killed"
	bgFailed  bgStatus = "failed" // never started (spawn error)
)

// bgProc is one background shell: the running command plus a bounded,
// mutex-guarded capture of its combined output and terminal state.
type bgProc struct {
	id      string
	command string
	started time.Time

	mu       sync.Mutex
	buf      capBuffer
	status   bgStatus
	exitCode int
	exitErr  string
	ended    time.Time
}

// snapshot returns a consistent view for shell_output without exposing
// the lock.
func (b *bgProc) snapshot() (status bgStatus, out string, truncated bool, exitCode int, exitErr string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.status, b.buf.String(), b.buf.truncated, b.exitCode, b.exitErr
}

// capBuffer is an io.Writer that keeps only the last shellMaxOutput bytes.
// A background command may print megabytes (a server log, a verbose
// install) — we keep the tail, which is what matters for diagnosis, and
// flag that earlier output was dropped.
type capBuffer struct {
	data      []byte
	truncated bool
}

func (c *capBuffer) Write(p []byte) (int, error) {
	c.data = append(c.data, p...)
	if len(c.data) > shellMaxOutput {
		c.data = c.data[len(c.data)-shellMaxOutput:]
		c.truncated = true
	}
	return len(p), nil
}

func (c *capBuffer) String() string { return string(c.data) }

// lockedWriter adapts a bgProc so cmd.Stdout/Stderr writes land in the
// capped buffer under the proc's mutex (the reaper reads it concurrently).
type lockedWriter struct{ b *bgProc }

func (w lockedWriter) Write(p []byte) (int, error) {
	w.b.mu.Lock()
	defer w.b.mu.Unlock()
	return w.b.buf.Write(p)
}

// bgRegistry holds the background shells for one spawn. Bounded by
// maxBgProcs so a runaway agent can't spawn unlimited detached processes.
type bgRegistry struct {
	workspace string
	// memLimitMB bounds each background shell's process tree. 0 = no
	// limit. Background shells matter more than foreground ones here:
	// they have no timeout, so a leaking watcher grows unbounded for as
	// long as the session lives.
	memLimitMB int

	mu    sync.Mutex
	procs map[string]*bgProc
	cmds  map[string]*exec.Cmd // for kill/release; separate so bgProc stays lock-simple
	scope map[string]string    // bash_id -> systemd scope unit, "" when unwrapped
}

// maxBgProcs caps concurrent background shells per session. Generous
// enough for real workflows (a dev server + an install + a watcher) while
// bounding the blast radius of a loop that spawns without killing.
const maxBgProcs = 16

func newBgRegistry(workspace string, memLimitMB int) *bgRegistry {
	return &bgRegistry{
		workspace:  workspace,
		memLimitMB: memLimitMB,
		procs:      map[string]*bgProc{},
		cmds:       map[string]*exec.Cmd{},
		scope:      map[string]string{},
	}
}

// start launches cmdline detached and returns its bash_id. The command
// runs to completion on its own; a reaper goroutine records the exit
// state. Returns an error string (not a Go error) shaped for the tool
// result.
func (r *bgRegistry) start(cmdline string) (string, error) {
	bin, err := resolveBash()
	if err != nil {
		return "", err
	}

	r.mu.Lock()
	if len(r.procs) >= maxBgProcs {
		r.mu.Unlock()
		return "", fmt.Errorf("too many background shells (max %d); kill some with shell_kill first", maxBgProcs)
	}
	id := "bash_" + uuid.NewString()[:8]
	bp := &bgProc{id: id, command: cmdline, started: time.Now(), status: bgRunning}
	r.procs[id] = bp
	r.mu.Unlock()

	execBin, execArgs, scopeUnit := wrapToolCmd(bin, []string{"-c", cmdline}, r.memLimitMB, nextToolSeq())
	cmd := safeexec.Command(execBin, execArgs...)
	if r.workspace != "" {
		cmd.Dir = r.workspace
	}
	w := lockedWriter{b: bp}
	cmd.Stdout = w
	cmd.Stderr = w

	proctree.Apply(cmd)
	if err := cmd.Start(); err != nil {
		bp.mu.Lock()
		bp.status = bgFailed
		bp.exitErr = err.Error()
		bp.ended = time.Now()
		bp.mu.Unlock()
		return "", err
	}
	proctree.Assign(cmd)

	r.mu.Lock()
	r.cmds[id] = cmd
	r.scope[id] = scopeUnit
	r.mu.Unlock()

	go r.reap(id, cmd, bp)
	log.Debug().Str("bash_id", id).Str("command", cmdline).Msg("wick.shell.bg: started")
	return id, nil
}

// reap waits for the background command and records its terminal state.
// A kill (shell_kill / session teardown) flips status to bgKilled first;
// reap must not overwrite that with a spurious "exited".
func (r *bgRegistry) reap(id string, cmd *exec.Cmd, bp *bgProc) {
	waitErr := cmd.Wait()
	proctree.Release(cmd)

	// Read the scope's accounting before anything reaps it: a memory kill
	// is indistinguishable from any other SIGKILL by exit status alone,
	// and "exit status 137" tells the model nothing it can act on.
	oomKilled := false
	if waitErr != nil {
		r.mu.Lock()
		unit := r.scope[id]
		r.mu.Unlock()
		oomKilled = toolOOMKilled(unit)
	}

	bp.mu.Lock()
	if bp.status == bgKilled {
		// Already marked killed by shell_kill; keep it.
		bp.ended = time.Now()
		bp.mu.Unlock()
	} else if oomKilled {
		bp.status = bgExited
		bp.exitErr = toolOOMMessage(r.memLimitMB)
		bp.exitCode = 137
		bp.ended = time.Now()
		bp.mu.Unlock()
	} else if waitErr != nil {
		bp.status = bgExited
		bp.exitErr = waitErr.Error()
		if ec, ok := waitErr.(interface{ ExitCode() int }); ok {
			bp.exitCode = ec.ExitCode()
		} else {
			bp.exitCode = -1
		}
		bp.ended = time.Now()
		bp.mu.Unlock()
	} else {
		bp.status = bgExited
		bp.exitCode = 0
		bp.ended = time.Now()
		bp.mu.Unlock()
	}

	r.mu.Lock()
	delete(r.cmds, id)
	delete(r.scope, id)
	r.mu.Unlock()
	log.Debug().Str("bash_id", id).Msg("wick.shell.bg: reaped")
}

// output returns a snapshot of a background shell for shell_output.
func (r *bgRegistry) output(id string) (*bgProc, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	bp, ok := r.procs[id]
	return bp, ok
}

// kill terminates a background shell's whole process tree. Idempotent —
// killing an already-finished shell is a no-op that still reports ok.
func (r *bgRegistry) kill(id string) error {
	r.mu.Lock()
	bp, ok := r.procs[id]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("no background shell with bash_id %q", id)
	}
	cmd := r.cmds[id]
	r.mu.Unlock()

	bp.mu.Lock()
	if bp.status == bgRunning {
		bp.status = bgKilled
	}
	bp.mu.Unlock()

	if cmd != nil {
		return proctree.Kill(cmd)
	}
	return nil
}

// killAll terminates every running background shell. Called on spawn
// teardown so detached processes don't outlive the session.
func (r *bgRegistry) killAll() {
	r.mu.Lock()
	cmds := make([]*exec.Cmd, 0, len(r.cmds))
	for _, c := range r.cmds {
		cmds = append(cmds, c)
	}
	r.mu.Unlock()
	for _, c := range cmds {
		_ = proctree.Kill(c)
	}
}

// ── tools ────────────────────────────────────────────────────────────

// shellOutputTool reports a background shell's captured output + status.
func shellOutputTool(tc toolContext) toolDef {
	return toolDef{
		decl: &genai.FunctionDeclaration{
			Name: "shell_output",
			Description: "Read the current combined stdout+stderr and status of a background " +
				"shell started with shell(run_in_background=true). Status is one of running / " +
				"exited / killed / failed; exit_code is set once it ends. Output is the tail " +
				"only if very long. Poll this until status is no longer running.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"bash_id": {
						Type:        genai.TypeString,
						Description: "The bash_id returned by the background shell call.",
					},
				},
				Required: []string{"bash_id"},
			},
		},
		handler: func(_ context.Context, args map[string]any) (string, bool) {
			if tc.Bg == nil {
				return "error: background shells are not available in this session", true
			}
			id, _ := args["bash_id"].(string)
			id = strings.TrimSpace(id)
			if id == "" {
				return "error: bash_id is required", true
			}
			bp, ok := tc.Bg.output(id)
			if !ok {
				return fmt.Sprintf("error: no background shell with bash_id %q", id), true
			}
			status, out, truncated, exitCode, exitErr := bp.snapshot()
			var sb strings.Builder
			fmt.Fprintf(&sb, "status: %s\n", status)
			if status != bgRunning {
				fmt.Fprintf(&sb, "exit_code: %d\n", exitCode)
				if exitErr != "" {
					fmt.Fprintf(&sb, "exit_error: %s\n", exitErr)
				}
			}
			sb.WriteString("--- output ---\n")
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

// shellKillTool stops a background shell (and its whole process tree).
func shellKillTool(tc toolContext) toolDef {
	return toolDef{
		decl: &genai.FunctionDeclaration{
			Name: "shell_kill",
			Description: "Stop a background shell started with shell(run_in_background=true), " +
				"terminating its whole process tree. Idempotent — killing an already-finished " +
				"shell still succeeds.",
			Parameters: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"bash_id": {
						Type:        genai.TypeString,
						Description: "The bash_id returned by the background shell call.",
					},
				},
				Required: []string{"bash_id"},
			},
		},
		handler: func(_ context.Context, args map[string]any) (string, bool) {
			if tc.Bg == nil {
				return "error: background shells are not available in this session", true
			}
			id, _ := args["bash_id"].(string)
			id = strings.TrimSpace(id)
			if id == "" {
				return "error: bash_id is required", true
			}
			if err := tc.Bg.kill(id); err != nil {
				return fmt.Sprintf("error: %v", err), true
			}
			return fmt.Sprintf("killed background shell %s", id), false
		},
	}
}
