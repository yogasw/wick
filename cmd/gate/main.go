// Command gate is the small binary claude's PreToolUse hook invokes
// before every Bash tool call. It reads a JSON envelope from stdin,
// decides allow/block via the gate matcher, logs the decision to
// the shared commands.jsonl, and signals claude via stdout JSON.
//
// Block contract (per Claude Code docs):
//
//	exit 0, no stdout JSON      → allow
//	exit 0, stdout JSON with
//	  hookSpecificOutput
//	  .permissionDecision="deny" → block (claude cancels the tool call)
//
// Note: exit 2 alone is NOT a block — claude ignores any stdout JSON
// when the exit code is non-zero, so the tool would still run. We
// always exit 0 from this binary.
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
	"path/filepath"
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

// emitBlock writes the PreToolUse deny envelope to stdout and the
// human-readable reason to stderr.
//
// Per Claude Code docs: JSON output is processed ONLY on exit 0.
// Exit 2 makes claude ignore the JSON and the tool still runs (we
// hit this on Windows with claude 2.1.138). Callers therefore pair
// this with `return 0`, not `return 2`. The deny is conveyed via
// hookSpecificOutput.permissionDecision="deny".
func emitBlock(reason string) {
	payload := map[string]any{
		"continue":   false,
		"stopReason": reason,
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "deny",
			"permissionDecisionReason": reason,
		},
	}
	if data, err := json.Marshal(payload); err == nil {
		fmt.Fprintln(os.Stdout, string(data))
	}
	fmt.Fprintf(os.Stderr, "gate: blocked — %s\n", reason)
}

// emitAllow writes the PreToolUse allow envelope, telling claude to
// skip the permission prompt and run the tool directly.
//
// Why this is mandatory: if we exit 0 with no JSON, claude falls
// back to its built-in permission flow. In headless `claude -p`
// runs there's no UI to prompt, so the tool either hangs or gets
// sandbox-blocked even though our gate already approved it. The
// allow envelope short-circuits that path.
//
// Pair with `return 0`. Reason is shown in claude's audit log; "" is
// fine for non-noteworthy approvals (whitelist match, auto-approved).
func emitAllow(reason string) {
	payload := map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "allow",
			"permissionDecisionReason": reason,
		},
	}
	if data, err := json.Marshal(payload); err == nil {
		fmt.Fprintln(os.Stdout, string(data))
	}
}

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
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--config", "-config", "config":
			printConfig()
			return
		case "--probe-deny":
			// Always-deny mode for ProbeGateSupport. Drains stdin so
			// the provider's hook write doesn't block, then emits the
			// deny envelope (per provider's contract) + exits 0. If
			// the provider honors the contract the tool is cancelled;
			// if not, the probe sees the side effect and reports
			// unsupported.
			//
			// Optional `--provider=<name>` second arg picks the deny
			// envelope shape. Empty / absent → claude shape (default
			// for backward compat with existing claude-only callers).
			providerName := parseProviderArg(os.Args[2:])
			_, _ = io.Copy(io.Discard, os.Stdin)
			emitBlockForProvider(providerName, "probe: forced deny")
			return
		}
	}
	exitCode := run()
	os.Exit(exitCode)
}

// parseProviderArg extracts the value of --provider=X (or --provider X)
// from a list of args, returning "" when absent. Tolerates unknown
// flags to keep the parser permissive — we only care about one key.
func parseProviderArg(args []string) string {
	for i, a := range args {
		switch {
		case strings.HasPrefix(a, "--provider="):
			return strings.TrimPrefix(a, "--provider=")
		case a == "--provider" && i+1 < len(args):
			return args[i+1]
		}
	}
	return ""
}

// emitBlockForProvider writes the deny envelope in the format the
// named provider expects. Falls back to claude's shape when name is
// empty or unknown — claude is the canonical reference and was the
// only supported provider before the multi-provider refactor, so
// preserving that shape keeps existing `ProbeGateSupport` callers
// working without changes.
func emitBlockForProvider(name, reason string) {
	switch name {
	case "codex":
		// Codex accepts a flat permissionDecision object with reason.
		// Per OpenAI's hooks doc (codex 0.129) exit-0 + JSON is the
		// safe path; exit 2 + stderr also works but is harder to test.
		payload := map[string]any{
			"permissionDecision": "deny",
			"reason":             reason,
		}
		if data, err := json.Marshal(payload); err == nil {
			fmt.Fprintln(os.Stdout, string(data))
		}
		fmt.Fprintf(os.Stderr, "gate: blocked — %s\n", reason)
	case "gemini":
		// Per public docs gemini reads `decision: deny` with reason.
		// UNVERIFIED — flip the shape once an integrator validates.
		payload := map[string]any{
			"decision": "deny",
			"reason":   reason,
		}
		if data, err := json.Marshal(payload); err == nil {
			fmt.Fprintln(os.Stdout, string(data))
		}
		fmt.Fprintf(os.Stderr, "gate: blocked — %s\n", reason)
	default:
		emitBlock(reason)
	}
}

// printConfig dumps resolved app name and every path the gate writes
// to. Use when the hook fires but data lands in the wrong tree —
// usually means BuildAppName ldflag wasn't injected and gate fell
// back to "wick" instead of e.g. "wick-lab".
func printConfig() {
	app := gate.AppName()
	exe, _ := os.Executable()
	home, _ := os.UserHomeDir()

	fmt.Printf("app_name:         %s\n", app)
	fmt.Printf("executable:       %s\n", exe)
	fmt.Printf("home:             %s\n", home)
	fmt.Printf("spec:             %s\n", gate.SharedSpecPath(app))
	fmt.Printf("socket:           %s\n", gate.SharedSocketPath(app))
	fmt.Printf("commands_jsonl:   %s\n", gate.SharedCommandsPath(app))
	fmt.Printf("daily_log_dir:    %s\n", filepath.Join(home, "."+app, "logs"))

	if st, err := os.Stat(gate.SharedSpecPath(app)); err == nil {
		fmt.Printf("spec_size:        %d bytes (mtime %s)\n", st.Size(), st.ModTime().UTC().Format(time.RFC3339))
	} else {
		fmt.Printf("spec_size:        MISSING (%v)\n", err)
	}
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
		emitBlock("stdin parse: " + perr.Error())
		return 0
	}

	if in.ToolName != "Bash" {
		return runPathGate(requestID, spec, in)
	}

	cmd := in.ToolInput.Command
	if cmd == "" {
		logTerminalEntry(requestID, "Bash", "", in.CWD, "blocked", "", "empty command")
		emitBlock("empty command")
		return 0
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
		emitAllow("whitelist")
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
		emitAllow("auto_approved")
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
		// Fail-open: when the daemon socket is unavailable the wick
		// server isn't running (or this isn't a wick session).
		// Allow the command rather than blocking the user's shell.
		gate.LogDaily(app, "warn", "approval rpc unavailable (allowed)", map[string]any{
			"request_id": requestID,
			"cmd":        cmd,
			"error":      err.Error(),
		})
		logTerminalEntry(requestID, "Bash", cmd, cwd, "allowed", "no_socket", err.Error())
		emitAllow("no_socket")
		return 0
	}
	if gate.IsApprove(decision) {
		gate.LogDaily(app, "info", "allowed via "+decision, map[string]any{
			"request_id": requestID,
			"cmd":        cmd,
			"reason":     reason,
		})
		logTerminalEntry(requestID, "Bash", cmd, cwd, "allowed", decision, reason)
		emitAllow(decision)
		return 0
	}
	gate.LogDaily(app, "warn", "blocked: "+decision, map[string]any{
		"request_id": requestID,
		"cmd":        cmd,
		"reason":     reason,
	})
	logTerminalEntry(requestID, "Bash", cmd, cwd, "blocked", decision, reason)
	emitBlock(reason)
	return 0
}

// runPathGate handles non-Bash tool calls (Read, Write, Edit, Glob, MCP, etc.).
// File tools enforce scope restriction; unknown/MCP tools always go to
// interactive approval so the user sees every tool call.
func runPathGate(requestID string, spec gate.Spec, in hookInput) int {
	path := pathFromInput(in)
	tool := in.ToolName

	// Unknown tool (MCP or future tool) — no path to scope-check.
	// Always ask the user rather than silently allowing.
	knownFileTool := tool == "Read" || tool == "Write" || tool == "Edit" || tool == "Glob" || tool == "LS"
	if !knownFileTool {
		key := gate.MatchKey(tool, path)
		if gate.IsAutoApproved(spec, key) {
			logTerminalEntry(requestID, tool, path, in.CWD, "allowed", "auto_approved", "")
			emitAllow("auto_approved")
			return 0
		}
		socketPath := gate.SharedSocketPath(gate.AppName())
		decision, reason, err := requestApprovalWithLog(socketPath, tool, path, in.CWD, in.SessionID, key, requestID)
		if err != nil {
			logTerminalEntry(requestID, tool, path, in.CWD, "allowed", "no_socket", err.Error())
			emitAllow("no_socket")
			return 0
		}
		if gate.IsApprove(decision) {
			logTerminalEntry(requestID, tool, path, in.CWD, "allowed", decision, reason)
			emitAllow(decision)
			return 0
		}
		logTerminalEntry(requestID, tool, path, in.CWD, "blocked", decision, reason)
		emitBlock(fmt.Sprintf("%s — %s", tool, reason))
		return 0
	}

	// Known file tool: no path extracted — nothing to scope-check, allow.
	if path == "" {
		logTerminalEntry(requestID, tool, "", in.CWD, "allowed", "no_path", "")
		emitAllow("no_path")
		return 0
	}
	// Relative path: resolve against CWD before scope check so traversal
	// sequences like "../../etc/passwd" are caught by PathWithinScope.
	// Use filepath.IsAbs for cross-platform: Windows paths (C:\...) are
	// absolute but don't start with "/", so HasPrefix alone misses them.
	if !strings.HasPrefix(path, "/") && !filepath.IsAbs(path) {
		if in.CWD != "" {
			path = filepath.Clean(filepath.Join(in.CWD, path))
		}
		// If CWD is empty the path stays relative; scope check below
		// treats it as within-scope (safe default when workspace unknown).
	}

	// Within default scope → allow immediately.
	if spec.DefaultScope != "" && gate.PathWithinScope(path, spec.DefaultScope) {
		logTerminalEntry(requestID, tool, path, in.CWD, "allowed", "scope", "")
		emitAllow("scope")
		return 0
	}

	// Auto-approved.
	key := gate.MatchKey(tool, path)
	if gate.IsAutoApproved(spec, key) {
		logTerminalEntry(requestID, tool, path, in.CWD, "allowed", "auto_approved", "")
		emitAllow("auto_approved")
		return 0
	}

	// Interactive approval via shared socket.
	socketPath := gate.SharedSocketPath(gate.AppName())
	decision, reason, err := requestApprovalWithLog(socketPath, tool, path, in.CWD, in.SessionID, key, requestID)
	if err != nil {
		// Fail-open: socket unavailable means wick not running.
		logTerminalEntry(requestID, tool, path, in.CWD, "allowed", "no_socket", err.Error())
		emitAllow("no_socket")
		return 0
	}
	if gate.IsApprove(decision) {
		logTerminalEntry(requestID, tool, path, in.CWD, "allowed", decision, reason)
		emitAllow(decision)
		return 0
	}
	logTerminalEntry(requestID, tool, path, in.CWD, "blocked", decision, reason)
	emitBlock(fmt.Sprintf("%s(%q) — %s", tool, path, reason))
	return 0
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
