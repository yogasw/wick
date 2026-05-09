// Command wick-gate is the small binary claude's PreToolUse hook
// invokes before every Bash tool call. It reads a JSON envelope from
// stdin, decides allow/block via the gate matcher, logs the decision
// to commands.jsonl, and exits with the claude-expected code:
//
//	exit 0 = allow
//	exit 2 = block (claude cancels the tool call)
//
// Configuration is loaded from the file path in $WICK_GATE_SPEC,
// which the wick parent process writes per spawn (see
// gate.WriteSpawnArtifacts). All decision state — rules, session
// commands.jsonl path, agent name — flows through that spec, so the
// binary itself stays stateless.
//
// Fail-safe: if anything goes wrong (spec missing, parse failure,
// timeout reading stdin), we BLOCK + log. Better to refuse a real
// command than let an unverified one through.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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

func main() {
	exitCode := run()
	os.Exit(exitCode)
}

// run is split out so tests can drive the same logic without
// invoking os.Exit. Returns the exit code we'd give claude.
func run() int {
	spec, err := gate.LoadSpec()
	if err != nil {
		// Without a spec we can't log anywhere meaningful — emit to
		// stderr so the operator sees the misconfiguration.
		fmt.Fprintf(os.Stderr, "wick-gate: %v\n", err)
		return 2
	}

	cmd, perr := readHookCommand(os.Stdin, stdinReadTimeout)
	if perr != nil {
		appendLog(spec, "", "blocked", "stdin parse: "+perr.Error())
		fmt.Fprintf(os.Stderr, "wick-gate: %v\n", perr)
		return 2
	}

	matcher := gate.NewMatcher(spec.Rules)
	allow, reason := matcher.Decide(cmd)
	if allow {
		appendLog(spec, cmd, "allowed", "")
		return 0
	}
	appendLog(spec, cmd, "blocked", reason)
	// Print the reason so claude can surface it back to the model.
	fmt.Fprintf(os.Stderr, "wick-gate: blocked — %s\n", reason)
	return 2
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

// appendLog writes one Entry to the session's commands.jsonl. Errors
// are intentionally swallowed — failure to log shouldn't block a
// command that would otherwise allow, and it can't make a block more
// severe.
func appendLog(spec gate.Spec, cmd, status, reason string) {
	entry := gate.Entry{
		Timestamp: time.Now().UTC(),
		Agent:     spec.AgentName,
		Cmd:       cmd,
		Status:    status,
		Reason:    reason,
	}
	// We were given the absolute path in spec.Layout — append directly
	// rather than threading config.Layout through.
	_ = storage.AppendJSONL(spec.Layout.SessionCommandsPath, "wick-cmd-v1", spec.SessionID, entry)
}
