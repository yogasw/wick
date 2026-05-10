// Command gate is the small binary claude's PreToolUse hook invokes
// before every Bash tool call. It reads a JSON envelope from stdin,
// decides allow/block via the gate matcher, logs the decision to
// the shared commands.jsonl, and exits with the claude-expected code:
//
//	exit 0 = allow
//	exit 2 = block (claude cancels the tool call)
//
// The binary ships per-app as `<app>-gate[.exe]` (e.g. `myapp-gate`
// for a project initialized with `wick init myapp`).
//
// Configuration is loaded from the shared spec at
// ~/.<app>/agents/gate/spec.json — the gate binary derives `<app>`
// from its own executable filename via gate.AppName() (strip
// `-gate[.exe]` suffix). No env var, no ldflag injection.
//
// Fail-safe: if anything goes wrong (spec missing, parse failure,
// timeout reading stdin), we BLOCK + log. Better to refuse a real
// command than let an unverified one through.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/gate"
)

// hookInput is the shape claude's PreToolUse hook sends on stdin.
// We model only the fields we use; unknown fields are ignored. The
// `cwd` field is what the daemon uses to route the approval back to
// the right wick session — gate itself stays session-agnostic.
type hookInput struct {
	HookEventName string        `json:"hook_event_name"`
	SessionID     string        `json:"session_id"` // claude session id (informational)
	CWD           string        `json:"cwd"`
	ToolName      string        `json:"tool_name"`
	ToolInput     hookToolInput `json:"tool_input"`
}

type hookToolInput struct {
	Command  string `json:"command"`   // Bash
	FilePath string `json:"file_path"` // Read, Write, Edit
	Pattern  string `json:"pattern"`   // Glob
	Path     string `json:"path"`      // Glob base / LS
}

// stdinReadTimeout is the upper bound for waiting on hook stdin. If
// claude doesn't deliver the JSON within this window, fail-safe
// kicks in and we block.
const stdinReadTimeout = 3 * time.Second

// socketDialTimeout caps the time we'll wait for the daemon's socket
// to accept our connection. Short — if the daemon is up at all the
// connect is sub-millisecond; if it's not we'd rather fail-safe
// quickly than stall claude's hook.
const socketDialTimeout = 2 * time.Second

// socketResponseTimeout is the upper bound for waiting on the daemon
// reply. Daemon-side default is 25s; we allow a little extra so a
// borderline-slow user click still races our exit.
const socketResponseTimeout = 28 * time.Second

func main() {
	exitCode := run()
	os.Exit(exitCode)
}

// run is split out so tests can drive the same logic without
// invoking os.Exit. Returns the exit code we'd give claude.
//
// Logging strategy: every stage of the decision flow emits one
// commands.jsonl entry. The terminal entry (status=allowed|blocked)
// is what the UI shows; intermediate "stage=..." entries are the
// audit trail an operator walks when behavior looks wrong. All
// entries share the same RequestID so a grep gives the full story
// of one command.
func run() int {
	requestID := newRequestID()
	app := gate.AppName()

	// Tail-log the invocation entry point before doing anything else
	// so operators can see "gate fired" even when later steps fail
	// (spec missing, stdin parse error, etc.).
	gate.LogDaily(app, "info", "gate invoked", map[string]any{
		"request_id": requestID,
		"pid":        os.Getpid(),
	})

	spec, err := gate.LoadSpec(app)
	if err != nil {
		gate.LogDaily(app, "error", "load spec", map[string]any{
			"request_id": requestID,
			"error":      err.Error(),
		})
		fmt.Fprintf(os.Stderr, "gate: load spec: %v\n", err)
		return 2
	}

	logStage(requestID, "received", "", "", "", "")

	in, perr := readHookInput(os.Stdin, stdinReadTimeout)
	if perr != nil {
		gate.LogDaily(app, "warn", "stdin parse blocked", map[string]any{
			"request_id": requestID,
			"error":      perr.Error(),
		})
		logTerminalEntry(requestID, "Bash", "", "", "blocked", "", "stdin parse: "+perr.Error())
		fmt.Fprintf(os.Stderr, "gate: %v\n", perr)
		return 2
	}

	if in.ToolName != "Bash" {
		return runPathGate(requestID, spec, in)
	}

	cmd := in.ToolInput.Command
	if cmd == "" {
		logTerminalEntry(requestID, "Bash", "", in.CWD, "blocked", "", "empty command")
		return 2
	}
	cwd := in.CWD
	claudeSID := in.SessionID

	// Whitelist match — fastest happy path.
	matcher := gate.NewMatcher(spec.Rules, spec.DefaultScope)
	if allow, _ := matcher.Decide(cmd); allow {
		gate.LogDaily(app, "info", "allowed via whitelist", map[string]any{
			"request_id": requestID,
			"cmd":        cmd,
		})
		logTerminalEntry(requestID, "Bash", cmd, cwd, "allowed", "whitelist", "")
		return 0
	}

	// Auto-approved (user clicked "Always allow" earlier). Same
	// zero-latency path as whitelist; daemon doesn't even need to
	// be running.
	key := gate.MatchKey("Bash", cmd)
	if gate.IsAutoApproved(spec, key) {
		gate.LogDaily(app, "info", "allowed via auto_approved", map[string]any{
			"request_id": requestID,
			"cmd":        cmd,
			"match_key":  key,
		})
		logTerminalEntry(requestID, "Bash", cmd, cwd, "allowed", "auto_approved", "")
		return 0
	}

	// Interactive approval — dial the shared daemon socket.
	socketPath := gate.SharedSocketPath(app)
	gate.LogDaily(app, "info", "dial daemon", map[string]any{
		"request_id": requestID,
		"cmd":        cmd,
		"socket":     socketPath,
	})
	logStage(requestID, "socket_dial", cmd, cwd, "", socketPath)
	decision, reason, err := requestApprovalWithLog(socketPath, "Bash", cmd, cwd, claudeSID, key, requestID)
	if err != nil {
		gate.LogDaily(app, "warn", "approval rpc failed (blocked)", map[string]any{
			"request_id": requestID,
			"cmd":        cmd,
			"error":      err.Error(),
		})
		logTerminalEntry(requestID, "Bash", cmd, cwd, "blocked", "", "approval rpc: "+err.Error())
		fmt.Fprintf(os.Stderr, "gate: blocked — approval rpc: %v\n", err)
		return 2
	}
	if gate.IsApprove(decision) {
		gate.LogDaily(app, "info", "allowed via "+decision, map[string]any{
			"request_id": requestID,
			"cmd":        cmd,
			"reason":     reason,
		})
		logTerminalEntry(requestID, "Bash", cmd, cwd, "allowed", decision, reason)
		return 0
	}
	gate.LogDaily(app, "warn", "blocked: "+decision, map[string]any{
		"request_id": requestID,
		"cmd":        cmd,
		"reason":     reason,
	})
	logTerminalEntry(requestID, "Bash", cmd, cwd, "blocked", decision, reason)
	fmt.Fprintf(os.Stderr, "gate: blocked — %s\n", reason)
	return 2
}

// runPathGate handles non-Bash tool calls (Read, Write, Edit, Glob).
// Only enforces scope restriction — no command whitelist applies.
// Within scope → allow. Outside scope → interactive approval or block.
func runPathGate(requestID string, spec gate.Spec, in hookInput) int {
	path := pathFromInput(in)
	tool := in.ToolName

	// No path extracted or relative path — safe (CWD = workspace).
	if path == "" || !strings.HasPrefix(path, "/") {
		logTerminalEntry(requestID, tool, path, in.CWD, "allowed", "relative_path", "")
		return 0
	}

	// Within default scope → allow immediately.
	if spec.DefaultScope != "" && gate.PathWithinScope(path, spec.DefaultScope) {
		logTerminalEntry(requestID, tool, path, in.CWD, "allowed", "scope", "")
		return 0
	}

	// Auto-approved.
	key := gate.MatchKey(tool, path)
	if gate.IsAutoApproved(spec, key) {
		logTerminalEntry(requestID, tool, path, in.CWD, "allowed", "auto_approved", "")
		return 0
	}

	// Interactive approval via shared socket.
	socketPath := gate.SharedSocketPath(gate.AppName())
	decision, reason, err := requestApprovalWithLog(socketPath, tool, path, in.CWD, in.SessionID, key, requestID)
	if err != nil {
		logTerminalEntry(requestID, tool, path, in.CWD, "blocked", "", "approval rpc: "+err.Error())
		fmt.Fprintf(os.Stderr, "gate: blocked %s(%q) — approval rpc: %v\n", tool, path, err)
		return 2
	}
	if gate.IsApprove(decision) {
		logTerminalEntry(requestID, tool, path, in.CWD, "allowed", decision, reason)
		return 0
	}
	logTerminalEntry(requestID, tool, path, in.CWD, "blocked", decision, reason)
	fmt.Fprintf(os.Stderr, "gate: blocked %s(%q) — %s\n", tool, path, reason)
	return 2
}

// logStage writes one intermediate audit-trail entry. Used to track
// progress through the decision flow before a terminal status is
// known. reason can hold extra metadata (e.g. socket path on dial).
func logStage(requestID, stage, cmd, cwd, decision, reason string) {
	entry := gate.Entry{
		Timestamp: time.Now().UTC(),
		Stage:     stage,
		Tool:      "Bash",
		Cmd:       cmd,
		WorkDir:   cwd,
		Decision:  decision,
		Reason:    reason,
		RequestID: requestID,
	}
	_ = gate.Append(gate.AppName(), entry)
}

// logTerminalEntry writes the final allowed/blocked entry for any tool.
// This is the row the UI Commands tab displays as the user-visible decision.
func logTerminalEntry(requestID, tool, cmd, cwd, status, decision, reason string) {
	key := ""
	if cmd != "" {
		key = gate.MatchKey(tool, cmd)
	}
	entry := gate.Entry{
		Timestamp: time.Now().UTC(),
		Tool:      tool,
		Cmd:       cmd,
		WorkDir:   cwd,
		Status:    status,
		Decision:  decision,
		Reason:    reason,
		RequestID: requestID,
		MatchKey:  key,
	}
	_ = gate.Append(gate.AppName(), entry)
}

// requestApprovalWithLog dials the shared daemon socket, sends one
// ApprovalRequest, and blocks for the reply. Each step (dial, send,
// recv) emits a `stage=...` entry so the operator can pinpoint
// exactly where a stuck approval got stuck. requestID ties all
// stages together; pass "" to skip the per-stage audit logging
// (used by tests). On any IO error the caller fail-safes to block.
// toolName is the Claude tool name (e.g. "Bash", "Read", "Glob").
func requestApprovalWithLog(socketPath, toolName, cmd, cwd, claudeSID, matchKey, requestID string) (decision, reason string, err error) {
	conn, err := net.DialTimeout("unix", socketPath, socketDialTimeout)
	if err != nil {
		if requestID != "" {
			logStage(requestID, "socket_error", cmd, cwd, "", "dial: "+err.Error())
		}
		return "", "", fmt.Errorf("dial %q: %w", socketPath, err)
	}
	defer conn.Close()

	socketReqID := newRequestID()
	req := gate.ApprovalRequest{
		ID:        socketReqID,
		SessionID: claudeSID,
		Tool:      toolName,
		Cmd:       cmd,
		WorkDir:   cwd,
		MatchKey:  matchKey,
		Timestamp: time.Now().UnixMilli(),
	}

	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		if requestID != "" {
			logStage(requestID, "socket_error", cmd, cwd, "", "send: "+err.Error())
		}
		return "", "", fmt.Errorf("send request: %w", err)
	}
	if requestID != "" {
		logStage(requestID, "socket_sent", cmd, cwd, "", "request_id="+socketReqID)
	}

	_ = conn.SetReadDeadline(time.Now().Add(socketResponseTimeout))
	var resp gate.ApprovalResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		if requestID != "" {
			logStage(requestID, "socket_error", cmd, cwd, "", "recv: "+err.Error())
		}
		return "", "", fmt.Errorf("read response: %w", err)
	}
	if requestID != "" {
		logStage(requestID, "socket_recv", cmd, cwd, resp.Decision, resp.Reason)
	}
	return resp.Decision, resp.Reason, nil
}

// newRequestID mints a 128-bit hex token. crypto/rand because the
// daemon uses the id as a map key and we want zero collisions per
// session.
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("ts-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// readHookInput reads the entire stdin payload (claude writes one
// JSON object then EOF), parses it, and returns the full hookInput.
// Caps wall-time via timeout.
func readHookInput(r io.Reader, timeout time.Duration) (hookInput, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	type result struct {
		data []byte
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(r)
		ch <- result{data, err}
	}()

	var data []byte
	select {
	case <-ctx.Done():
		return hookInput{}, fmt.Errorf("stdin read timeout after %v", timeout)
	case res := <-ch:
		if res.err != nil {
			return hookInput{}, fmt.Errorf("stdin read: %w", res.err)
		}
		data = res.data
	}

	if len(data) == 0 {
		return hookInput{}, fmt.Errorf("empty stdin")
	}
	var in hookInput
	if err := json.Unmarshal(data, &in); err != nil {
		return hookInput{}, fmt.Errorf("hook input parse: %w", err)
	}
	return in, nil
}

// pathFromInput extracts the primary filesystem path from a non-Bash tool's
// input. Returns "" when no path can be determined (relative pattern, etc.).
func pathFromInput(in hookInput) string {
	switch in.ToolName {
	case "Read", "Write", "Edit":
		return in.ToolInput.FilePath
	case "Glob":
		// Prefer explicit path (search root). Fall back to extracting the
		// non-wildcard prefix from the pattern.
		if in.ToolInput.Path != "" {
			return in.ToolInput.Path
		}
		p := in.ToolInput.Pattern
		if i := strings.IndexAny(p, "*?["); i >= 0 {
			p = p[:i]
		}
		return strings.TrimRight(p, "/")
	case "LS":
		return in.ToolInput.Path
	}
	return ""
}
