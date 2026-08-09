package main

import (
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/gate"
)

func TestIsAlwaysAllowedTool(t *testing.T) {
	allowed := []string{
		"AskUserQuestion",
		"ToolSearch",
		"mcp__wick-agent__ask_user",
		"mcp__wick-agent__wick_list",
		"mcp__wick-agent__wick_search",
		"mcp__wick-agent__wick_get",
		"mcp__wick-agent__wick_info",
		"mcp__wick-agent__wick_list_providers",
		"mcp__wick-agent__wick_skill_list",
		"mcp__wick-agent__wick_session_info",
		"mcp__wick-agent__wick_set_title",
		"mcp__wick-agent__wick_session_workspace",
	}
	for _, name := range allowed {
		if !isAlwaysAllowedTool(name) {
			t.Errorf("expected %q to be always-allowed", name)
		}
	}

	gated := []string{
		"Bash",
		"Read",
		"Write",
		"mcp__wick-agent__wick_execute", // real connector op — must stay gated
		"mcp__wick-agent__wick_skill_sync",
		"mcp__slack__send_message",
	}
	for _, name := range gated {
		if isAlwaysAllowedTool(name) {
			t.Errorf("expected %q to NOT be always-allowed", name)
		}
	}
}

func TestReadHookInputHappyPath(t *testing.T) {
	in := strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Bash","cwd":"/tmp/x","session_id":"abc","tool_input":{"command":"ls -la"}}`)
	got, err := readHookInput(in, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.ToolInput.Command != "ls -la" {
		t.Fatalf("cmd: %q", got.ToolInput.Command)
	}
	if got.CWD != "/tmp/x" {
		t.Fatalf("cwd: %q", got.CWD)
	}
	if got.SessionID != "abc" {
		t.Fatalf("session_id: %q", got.SessionID)
	}
}

func TestReadHookInputEmpty(t *testing.T) {
	in := strings.NewReader("")
	if _, err := readHookInput(in, time.Second); err == nil {
		t.Fatal("empty stdin should error")
	}
}

func TestReadHookInputMalformed(t *testing.T) {
	in := strings.NewReader("not json")
	if _, err := readHookInput(in, time.Second); err == nil {
		t.Fatal("malformed json should error")
	}
}

func TestReadHookInputMissingCommandField(t *testing.T) {
	// Non-Bash tools may omit command — parse should succeed now.
	// A Bash call with no command is caught in run(), not readHookInput.
	in := strings.NewReader(`{"hook_event_name":"PreToolUse","tool_name":"Read","tool_input":{"file_path":"/tmp/foo"}}`)
	got, err := readHookInput(in, time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.ToolName != "Read" {
		t.Fatalf("tool_name: %q", got.ToolName)
	}
	if got.ToolInput.FilePath != "/tmp/foo" {
		t.Fatalf("file_path: %q", got.ToolInput.FilePath)
	}
}

// blockingReader never returns — used to drive the timeout path.
type blockingReader struct{ ch chan struct{} }

func (b *blockingReader) Read(p []byte) (int, error) {
	<-b.ch
	return 0, nil
}

func TestReadHookInputTimeout(t *testing.T) {
	r := &blockingReader{ch: make(chan struct{})}
	defer close(r.ch)
	start := time.Now()
	_, err := readHookInput(r, 50*time.Millisecond)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("timeout took too long: %v", elapsed)
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout message, got %q", err.Error())
	}
}

// startFakeApprovalServer spins up a unix-socket listener that
// responds to one ApprovalRequest with the given decision.
func startFakeApprovalServer(t *testing.T, decision, reason string) string {
	t.Helper()
	sockPath := filepath.Join(t.TempDir(), "g.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var req gate.ApprovalRequest
		if err := json.NewDecoder(conn).Decode(&req); err != nil {
			return
		}
		_ = json.NewEncoder(conn).Encode(gate.ApprovalResponse{
			ID:       req.ID,
			Decision: decision,
			Reason:   reason,
		})
	}()
	return sockPath
}

func TestRequestApprovalApprove(t *testing.T) {
	sock := startFakeApprovalServer(t, gate.DecisionApproveOnce, "user clicked")
	dec, _, err := requestApprovalWithLog(sock, "Bash", "git status", "/cwd", "claude-sid", gate.MatchKey("Bash", "git status"), "")
	if err != nil {
		t.Fatalf("requestApproval: %v", err)
	}
	if dec != gate.DecisionApproveOnce {
		t.Errorf("decision: got %q, want %q", dec, gate.DecisionApproveOnce)
	}
}

func TestRequestApprovalBlock(t *testing.T) {
	sock := startFakeApprovalServer(t, gate.DecisionBlock, "user said no")
	dec, reason, err := requestApprovalWithLog(sock, "Bash", "rm -rf /", "/cwd", "", gate.MatchKey("Bash", "rm -rf /"), "")
	if err != nil {
		t.Fatalf("requestApproval: %v", err)
	}
	if dec != gate.DecisionBlock {
		t.Errorf("decision: got %q", dec)
	}
	if reason != "user said no" {
		t.Errorf("reason: got %q", reason)
	}
}

func TestRequestApprovalNoServer(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.sock")
	if _, _, err := requestApprovalWithLog(missing, "Bash", "ls", "/cwd", "", gate.MatchKey("Bash", "ls"), ""); err == nil {
		t.Fatal("expected dial error when socket file missing")
	}
}

func TestNewRequestIDUnique(t *testing.T) {
	seen := make(map[string]struct{})
	for i := 0; i < 50; i++ {
		id := newRequestID()
		if len(id) != 32 {
			t.Errorf("expected 32-hex id, got %q", id)
		}
		if _, dup := seen[id]; dup {
			t.Errorf("duplicate id: %q", id)
		}
		seen[id] = struct{}{}
	}
}

func TestIsAutoApprovedShortCircuit(t *testing.T) {
	cmd := "git push origin main"
	key := gate.MatchKey("Bash", cmd)
	spec := gate.Spec{AutoApproved: []string{key, "other-key"}}
	if !gate.IsAutoApproved(spec, key) {
		t.Errorf("IsAutoApproved should return true for key in list")
	}
	if gate.IsAutoApproved(spec, gate.MatchKey("Bash", "different")) {
		t.Errorf("IsAutoApproved should return false for key not in list")
	}
}

// ── Command extraction: any tool that runs shell text is gated as one ──

func TestCommandFromInput_Bash(t *testing.T) {
	in := decodeHook(t, `{"tool_name":"Bash","tool_input":{"command":"ls -la"}}`)
	if got := commandFromInput(in); got != "ls -la" {
		t.Errorf("got %q, want %q", got, "ls -la")
	}
}

// PowerShell is the regression: it carries a command but is not named
// Bash, so it used to reach the path gate with no path at all — no
// whitelist check, no metachar check, and one shared match key.
func TestCommandFromInput_PowerShell(t *testing.T) {
	in := decodeHook(t, `{"tool_name":"PowerShell","tool_input":{"command":"Remove-Item -Recurse 1111"}}`)
	if got := commandFromInput(in); got != "Remove-Item -Recurse 1111" {
		t.Errorf("got %q, want the PowerShell command", got)
	}
}

// A tool we do not model by name may still carry executable text under
// another key. Reading it is what keeps the prompt honest about what
// will run.
func TestCommandFromInput_UnmodelledKeys(t *testing.T) {
	for _, tc := range []struct{ name, payload, want string }{
		{"script", `{"tool_name":"X","tool_input":{"script":"rm -rf /"}}`, "rm -rf /"},
		{"cmd", `{"tool_name":"X","tool_input":{"cmd":"del /f"}}`, "del /f"},
		{"code", `{"tool_name":"X","tool_input":{"code":"print(1)"}}`, "print(1)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := commandFromInput(decodeHook(t, tc.payload)); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCommandFromInput_FileToolsAreNotCommands(t *testing.T) {
	in := decodeHook(t, `{"tool_name":"Read","tool_input":{"file_path":"/etc/passwd"}}`)
	if got := commandFromInput(in); got != "" {
		t.Errorf("file tool reported a command: %q", got)
	}
}

func TestCommandFromInput_BlankIsNotACommand(t *testing.T) {
	in := decodeHook(t, `{"tool_name":"PowerShell","tool_input":{"command":"   "}}`)
	if got := commandFromInput(in); got != "" {
		t.Errorf("whitespace-only command should not count: %q", got)
	}
}

func decodeHook(t *testing.T, payload string) hookInput {
	t.Helper()
	in, err := readHookInput(strings.NewReader(payload), time.Second)
	if err != nil {
		t.Fatalf("readHookInput: %v", err)
	}
	return in
}

// ── Deny envelope: two meanings of "no" ─────────────────────────────

// `continue: false` stops the agent's whole turn; permissionDecision
// alone refuses one call and hands the reason back so the agent can
// take a different route. Claude ignores permissionDecision whenever
// `continue: false` is present, so a guided refusal must not set both.
func TestEmitDeny_GuidedRefusalKeepsTurnAlive(t *testing.T) {
	out := captureStdout(t, func() { emitDeny("use git clean -n first", false) })

	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if _, ok := payload["continue"]; ok {
		t.Error("guided refusal sent `continue` — that halts the turn and voids permissionDecision")
	}
	if _, ok := payload["stopReason"]; ok {
		t.Error("guided refusal sent `stopReason`")
	}
	hook, ok := payload["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatalf("missing hookSpecificOutput in %q", out)
	}
	if hook["permissionDecision"] != "deny" {
		t.Errorf("permissionDecision: got %v, want deny", hook["permissionDecision"])
	}
	// The reason is the whole point: it reaches the model as feedback.
	if hook["permissionDecisionReason"] != "use git clean -n first" {
		t.Errorf("reason not carried through: got %v", hook["permissionDecisionReason"])
	}
}

func TestEmitDeny_HardBlockStopsTurn(t *testing.T) {
	out := captureStdout(t, func() { emitDeny("nope", true) })

	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if payload["continue"] != false {
		t.Errorf("hard block must set continue=false, got %v", payload["continue"])
	}
	if payload["stopReason"] != "nope" {
		t.Errorf("stopReason: got %v, want nope", payload["stopReason"])
	}
}

func TestEmitBlock_IsAHardBlock(t *testing.T) {
	out := captureStdout(t, func() { emitBlock("nope") })
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if payload["continue"] != false {
		t.Error("emitBlock must remain the turn-ending form")
	}
}
