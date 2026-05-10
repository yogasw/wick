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
// ~/.<app>/agents/gate/spec.json — the gate binary derives this path
// from the compile-time `gate.AppName` (injected via -ldflags by
// `wick build`). No runtime env var is consulted; this is the
// post-Stage 9 model.
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
	Command string `json:"command"`
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

	spec, err := gate.LoadSpec(gate.AppName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gate: load spec: %v\n", err)
		return 2
	}

	logStage(requestID, "received", "", "", "", "")

	cmd, cwd, claudeSID, perr := readHookInput(os.Stdin, stdinReadTimeout)
	if perr != nil {
		logTerminal(requestID, "", "", "blocked", "", "stdin parse: "+perr.Error())
		fmt.Fprintf(os.Stderr, "gate: %v\n", perr)
		return 2
	}

	// Whitelist match — fastest happy path.
	matcher := gate.NewMatcher(spec.Rules)
	if allow, _ := matcher.Decide(cmd); allow {
		logTerminal(requestID, cmd, cwd, "allowed", "whitelist", "")
		return 0
	}

	// Auto-approved (user clicked "Always allow" earlier). Same
	// zero-latency path as whitelist; daemon doesn't even need to
	// be running.
	key := gate.MatchKey("Bash", cmd)
	if gate.IsAutoApproved(spec, key) {
		logTerminal(requestID, cmd, cwd, "allowed", "auto_approved", "")
		return 0
	}

	// Interactive approval — dial the shared daemon socket.
	socketPath := gate.SharedSocketPath(gate.AppName)
	logStage(requestID, "socket_dial", cmd, cwd, "", socketPath)
	decision, reason, err := requestApprovalWithLog(socketPath, cmd, cwd, claudeSID, key, requestID)
	if err != nil {
		logTerminal(requestID, cmd, cwd, "blocked", "", "approval rpc: "+err.Error())
		fmt.Fprintf(os.Stderr, "gate: blocked — approval rpc: %v\n", err)
		return 2
	}
	if gate.IsApprove(decision) {
		logTerminal(requestID, cmd, cwd, "allowed", decision, reason)
		return 0
	}
	logTerminal(requestID, cmd, cwd, "blocked", decision, reason)
	fmt.Fprintf(os.Stderr, "gate: blocked — %s\n", reason)
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
	_ = gate.Append(gate.AppName, entry)
}

// logTerminal writes the final allowed/blocked entry. This is the
// row the UI Commands tab displays as the user-visible decision.
func logTerminal(requestID, cmd, cwd, status, decision, reason string) {
	key := ""
	if cmd != "" {
		key = gate.MatchKey("Bash", cmd)
	}
	entry := gate.Entry{
		Timestamp: time.Now().UTC(),
		Tool:      "Bash",
		Cmd:       cmd,
		WorkDir:   cwd,
		Status:    status,
		Decision:  decision,
		Reason:    reason,
		RequestID: requestID,
		MatchKey:  key,
	}
	_ = gate.Append(gate.AppName, entry)
}

// requestApprovalWithLog dials the shared daemon socket, sends one
// ApprovalRequest, and blocks for the reply. Each step (dial, send,
// recv) emits a `stage=...` entry so the operator can pinpoint
// exactly where a stuck approval got stuck. requestID ties all
// stages together; pass "" to skip the per-stage audit logging
// (used by tests). On any IO error the caller fail-safes to block.
func requestApprovalWithLog(socketPath, cmd, cwd, claudeSID, matchKey, requestID string) (decision, reason string, err error) {
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
		Tool:      "Bash",
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
// JSON object then EOF), parses it, and returns (cmd, cwd, claudeSID).
// Caps wall-time via timeout.
func readHookInput(r io.Reader, timeout time.Duration) (cmd, cwd, claudeSID string, err error) {
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
		return "", "", "", fmt.Errorf("stdin read timeout after %v", timeout)
	case res := <-ch:
		if res.err != nil {
			return "", "", "", fmt.Errorf("stdin read: %w", res.err)
		}
		data = res.data
	}

	if len(data) == 0 {
		return "", "", "", fmt.Errorf("empty stdin")
	}
	var in hookInput
	if err := json.Unmarshal(data, &in); err != nil {
		return "", "", "", fmt.Errorf("hook input parse: %w", err)
	}
	if in.ToolInput.Command == "" {
		return "", "", "", fmt.Errorf("hook input missing tool_input.command (tool=%q)", in.ToolName)
	}
	return in.ToolInput.Command, in.CWD, in.SessionID, nil
}
