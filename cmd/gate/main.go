// Command gate is the small binary claude's PreToolUse hook invokes
// before every Bash tool call. It reads a JSON envelope from stdin,
// decides allow/block via the gate matcher, logs the decision to
// commands.jsonl, and exits with the claude-expected code:
//
//	exit 0 = allow
//	exit 2 = block (claude cancels the tool call)
//
// The binary ships per-app as `<app>-gate[.exe]` (e.g. `myapp-gate`
// for a project initialized with `wick init myapp`).
//
// Configuration is loaded from the file path in $GATE_SPEC, which
// the parent process writes per spawn (see gate.WriteSpawnArtifacts).
// All decision state — rules, session commands.jsonl path, agent
// name — flows through that spec, so the binary itself stays
// stateless.
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
	"github.com/yogasw/wick/internal/agents/storage"
)

// hookInput is the shape claude's PreToolUse hook sends on stdin.
// We model only the fields we use; unknown fields are ignored.
type hookInput struct {
	HookEventName string         `json:"hook_event_name"`
	ToolName      string         `json:"tool_name"`
	ToolInput     hookToolInput  `json:"tool_input"`
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

	spec, err := gate.LoadSpec()
	if err != nil {
		// Spec missing → no SessionCommandsPath to write to. Stderr
		// is the only channel the operator sees.
		fmt.Fprintf(os.Stderr, "gate: %v\n", err)
		return 2
	}

	logStage(spec, requestID, "received", "", "", "")

	cmd, perr := readHookCommand(os.Stdin, stdinReadTimeout)
	if perr != nil {
		logTerminal(spec, requestID, "", "blocked", "", "stdin parse: "+perr.Error())
		fmt.Fprintf(os.Stderr, "gate: %v\n", perr)
		return 2
	}

	// Whitelist match — fastest happy path.
	matcher := gate.NewMatcher(spec.Rules)
	if allow, _ := matcher.Decide(cmd); allow {
		logTerminal(spec, requestID, cmd, "allowed", "whitelist", "")
		return 0
	}

	// Auto-approved (user clicked "Always allow" earlier). Same
	// zero-latency path as whitelist; daemon doesn't even need to
	// be running.
	key := gate.MatchKey("Bash", cmd)
	if gate.IsAutoApproved(spec, key) {
		logTerminal(spec, requestID, cmd, "allowed", "auto_approved", "")
		return 0
	}

	// Interactive approval — only attempt if a socket path was
	// configured. Whitelist-only deployments (no daemon) just block.
	if spec.SocketPath != "" {
		logStage(spec, requestID, "socket_dial", cmd, "", spec.SocketPath)
		decision, reason, err := requestApprovalWithLog(spec, cmd, key, requestID)
		if err != nil {
			logTerminal(spec, requestID, cmd, "blocked", "", "approval rpc: "+err.Error())
			fmt.Fprintf(os.Stderr, "gate: blocked — approval rpc: %v\n", err)
			return 2
		}
		if gate.IsApprove(decision) {
			logTerminal(spec, requestID, cmd, "allowed", decision, reason)
			return 0
		}
		logTerminal(spec, requestID, cmd, "blocked", decision, reason)
		fmt.Fprintf(os.Stderr, "gate: blocked — %s\n", reason)
		return 2
	}

	// No socket → fail-safe block. Reason mirrors the matcher's
	// rejection so the operator can see why a command failed.
	_, matcherReason := matcher.Decide(cmd)
	logTerminal(spec, requestID, cmd, "blocked", "", matcherReason)
	fmt.Fprintf(os.Stderr, "gate: blocked — %s\n", matcherReason)
	return 2
}

// logStage writes one intermediate audit-trail entry. Used to track
// progress through the decision flow before a terminal status is
// known. reason can hold extra metadata (e.g. socket path on dial).
func logStage(spec gate.Spec, requestID, stage, cmd, decision, reason string) {
	entry := gate.Entry{
		Timestamp: time.Now().UTC(),
		Stage:     stage,
		Agent:     spec.AgentName,
		Tool:      "Bash",
		Cmd:       cmd,
		Decision:  decision,
		Reason:    reason,
		RequestID: requestID,
	}
	_ = storage.AppendJSONL(spec.Layout.SessionCommandsPath, "wick-cmd-v1", spec.SessionID, entry)
}

// logTerminal writes the final allowed/blocked entry. This is the
// row the UI Commands tab displays as the user-visible decision.
func logTerminal(spec gate.Spec, requestID, cmd, status, decision, reason string) {
	key := ""
	if cmd != "" {
		key = gate.MatchKey("Bash", cmd)
	}
	entry := gate.Entry{
		Timestamp: time.Now().UTC(),
		Agent:     spec.AgentName,
		Tool:      "Bash",
		Cmd:       cmd,
		Status:    status,
		Decision:  decision,
		Reason:    reason,
		RequestID: requestID,
		MatchKey:  key,
	}
	_ = storage.AppendJSONL(spec.Layout.SessionCommandsPath, "wick-cmd-v1", spec.SessionID, entry)
}

// requestApproval dials the daemon's per-session unix socket, sends
// one ApprovalRequest, and blocks for the reply. On any IO error the
// caller fail-safes to block.
//
// Used only by tests now — production goes through requestApprovalWithLog
// which adds per-stage audit entries to commands.jsonl.
func requestApproval(spec gate.Spec, cmd, matchKey string) (decision, reason string, err error) {
	return requestApprovalWithLog(spec, cmd, matchKey, "")
}

// requestApprovalWithLog is requestApproval + audit logging. Each
// step (dial, send, recv) emits a `stage=...` entry so the operator
// can pinpoint exactly where a stuck approval got stuck. requestID
// ties all stages together; pass "" to skip logging (test path).
func requestApprovalWithLog(spec gate.Spec, cmd, matchKey, requestID string) (decision, reason string, err error) {
	conn, err := net.DialTimeout("unix", spec.SocketPath, socketDialTimeout)
	if err != nil {
		if requestID != "" {
			logStage(spec, requestID, "socket_error", cmd, "", "dial: "+err.Error())
		}
		return "", "", fmt.Errorf("dial %q: %w", spec.SocketPath, err)
	}
	defer conn.Close()

	socketReqID := newRequestID()
	req := gate.ApprovalRequest{
		ID:        socketReqID,
		SessionID: spec.SessionID,
		AgentName: spec.AgentName,
		Tool:      "Bash",
		Cmd:       cmd,
		WorkDir:   currentWorkDir(),
		MatchKey:  matchKey,
		Timestamp: time.Now().UnixMilli(),
	}

	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		if requestID != "" {
			logStage(spec, requestID, "socket_error", cmd, "", "send: "+err.Error())
		}
		return "", "", fmt.Errorf("send request: %w", err)
	}
	if requestID != "" {
		logStage(spec, requestID, "socket_sent", cmd, "", "request_id="+socketReqID)
	}

	_ = conn.SetReadDeadline(time.Now().Add(socketResponseTimeout))
	var resp gate.ApprovalResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		if requestID != "" {
			logStage(spec, requestID, "socket_error", cmd, "", "recv: "+err.Error())
		}
		return "", "", fmt.Errorf("read response: %w", err)
	}
	if requestID != "" {
		logStage(spec, requestID, "socket_recv", cmd, resp.Decision, resp.Reason)
	}
	return resp.Decision, resp.Reason, nil
}

// newRequestID mints a 128-bit hex token. crypto/rand because the
// daemon uses the id as a map key and we want zero collisions per
// session.
func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read effectively never fails on the platforms we
		// target; if it ever does, fall back to a timestamp so
		// fail-safe still works.
		return fmt.Sprintf("ts-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// currentWorkDir returns the cwd or an empty string. Best-effort
// metadata for the UI to render — a misread cwd shouldn't fail
// the request.
func currentWorkDir() string {
	if d, err := os.Getwd(); err == nil {
		return d
	}
	return ""
}

// readHookCommand reads the entire stdin payload (claude writes one
// JSON object then EOF), parses it, and returns the embedded command
// string. Caps wall-time via timeout.
func readHookCommand(r io.Reader, timeout time.Duration) (string, error) {
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
		return "", fmt.Errorf("stdin read timeout after %v", timeout)
	case res := <-ch:
		if res.err != nil {
			return "", fmt.Errorf("stdin read: %w", res.err)
		}
		data = res.data
	}

	if len(data) == 0 {
		return "", fmt.Errorf("empty stdin")
	}
	var in hookInput
	if err := json.Unmarshal(data, &in); err != nil {
		return "", fmt.Errorf("hook input parse: %w", err)
	}
	if in.ToolInput.Command == "" {
		return "", fmt.Errorf("hook input missing tool_input.command (tool=%q)", in.ToolName)
	}
	return in.ToolInput.Command, nil
}

