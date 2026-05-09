package gate_test

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/gate"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
)

// buildGate compiles cmd/gate to a temp file and returns the
// absolute path. Skips the test if `go build` is unavailable.
//
// We build per-test (not once for the package) so each scenario has
// a clean binary; build cost is ~1s on a warm cache.
func buildGate(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("`go` not in PATH — can't compile gate")
	}
	out := filepath.Join(t.TempDir(), "gate")
	if runtime.GOOS == "windows" {
		out += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", out, "github.com/yogasw/wick/cmd/gate")
	if buildOut, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build gate: %v\n%s", err, buildOut)
	}
	return out
}

// setupGate creates a session, writes a Spec + settings.json under
// gateDir, and returns (binPath, layout, gateDir).
func setupGate(t *testing.T, rules []gate.CommandRule) (string, config.Layout, string, string) {
	t.Helper()
	bin := buildGate(t)
	layout := config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID:     "S1",
		Origin: session.OriginUI,
	}); err != nil {
		t.Fatal(err)
	}

	gateDir := filepath.Join(layout.SessionDir("S1"), "gate")
	spec := gate.Spec{
		SessionID: "S1",
		AgentName: "default",
		Layout:    gate.SpecLayout{SessionCommandsPath: layout.SessionCommands("S1")},
		Rules:     rules,
	}
	_, specPath, err := gate.WriteSpawnArtifacts(gateDir, spec, bin)
	if err != nil {
		t.Fatal(err)
	}
	return bin, layout, gateDir, specPath
}

// runGate invokes the gate binary with the given stdin and
// $GATE_SPEC. Returns exit code and stderr text for assertions.
func runGate(t *testing.T, bin, specPath, stdin string) (int, string) {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = append([]string{}, "GATE_SPEC="+specPath, "PATH="+pathEnv())
	cmd.Stdin = strings.NewReader(stdin)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	cmd.Stdout = nil
	err := cmd.Run()
	exit := 0
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run gate: %v", err)
	}
	return exit, stderr.String()
}

// TestWickGate_Allow: a "ls *" rule lets `ls -la` through.
func TestWickGate_Allow(t *testing.T) {
	bin, layout, _, specPath := setupGate(t, []gate.CommandRule{{Pattern: "ls *"}})
	exit, _ := runGate(t, bin, specPath,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls -la"}}`)
	if exit != 0 {
		t.Fatalf("exit: got %d, want 0 (allow)", exit)
	}
	entries := terminalOnly(readCommands(t, layout, "S1"))
	if len(entries) != 1 || entries[0].Status != "allowed" || entries[0].Cmd != "ls -la" {
		t.Fatalf("commands.jsonl: %+v", entries)
	}
}

// TestWickGate_BlockUnlistedCommand: `rm -rf .` is not whitelisted.
func TestWickGate_BlockUnlistedCommand(t *testing.T) {
	bin, layout, _, specPath := setupGate(t, []gate.CommandRule{{Pattern: "ls *"}})
	exit, stderr := runGate(t, bin, specPath,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf ."}}`)
	if exit != 2 {
		t.Fatalf("exit: got %d, want 2 (block)", exit)
	}
	if !strings.Contains(stderr, "blocked") {
		t.Errorf("stderr should mention block: %q", stderr)
	}
	entries := terminalOnly(readCommands(t, layout, "S1"))
	if len(entries) != 1 || entries[0].Status != "blocked" || entries[0].Cmd != "rm -rf ." {
		t.Fatalf("commands.jsonl: %+v", entries)
	}
}

// TestWickGate_BlockShellMetacharOnAllowedRule: even a "git *"
// match must not let a piped command through.
func TestWickGate_BlockShellMetacharOnAllowedRule(t *testing.T) {
	bin, layout, _, specPath := setupGate(t, []gate.CommandRule{{Pattern: "git *"}})
	exit, _ := runGate(t, bin, specPath,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git status; rm -rf ."}}`)
	if exit != 2 {
		t.Fatalf("metachar should block: exit %d", exit)
	}
	entries := terminalOnly(readCommands(t, layout, "S1"))
	if len(entries) != 1 || !strings.Contains(entries[0].Reason, "metacharacter") {
		t.Fatalf("entry: %+v", entries)
	}
}

// TestWickGate_AuditTrailLogged: every invocation must emit a
// "received" stage + a terminal status row. Without the stage row,
// operators have no way to tell gate-fired-but-failed apart
// from gate-never-ran. The terminal row is what the UI shows;
// the stage row is the audit trail.
func TestWickGate_AuditTrailLogged(t *testing.T) {
	bin, layout, _, specPath := setupGate(t, []gate.CommandRule{{Pattern: "ls *"}})
	exit, _ := runGate(t, bin, specPath,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls -la"}}`)
	if exit != 0 {
		t.Fatalf("exit: got %d", exit)
	}

	all := readCommands(t, layout, "S1")
	var sawReceived, sawTerminal bool
	var requestID string
	for _, e := range all {
		if e.Stage == "received" {
			sawReceived = true
			requestID = e.RequestID
		}
		if e.Status == "allowed" {
			sawTerminal = true
			if e.RequestID != requestID && requestID != "" {
				t.Errorf("RequestID mismatch: stage=%q terminal=%q", requestID, e.RequestID)
			}
			if e.MatchKey == "" {
				t.Errorf("terminal entry missing MatchKey")
			}
			if e.Tool != "Bash" {
				t.Errorf("terminal entry tool: %q", e.Tool)
			}
		}
	}
	if !sawReceived {
		t.Errorf("expected at least one stage=received entry, got: %+v", all)
	}
	if !sawTerminal {
		t.Errorf("expected at least one terminal status=allowed entry, got: %+v", all)
	}
}

// TestWickGate_MalformedStdin: garbage input fails safe (block).
func TestWickGate_MalformedStdin(t *testing.T) {
	bin, _, _, specPath := setupGate(t, []gate.CommandRule{{Pattern: "ls *"}})
	exit, _ := runGate(t, bin, specPath, "not json")
	if exit != 2 {
		t.Fatalf("malformed should block: exit %d", exit)
	}
}

// TestWickGate_MissingSpecEnvFailsSafe: no env var → block.
func TestWickGate_MissingSpecEnvFailsSafe(t *testing.T) {
	bin := buildGate(t)
	cmd := exec.Command(bin)
	// Empty env — no GATE_SPEC.
	cmd.Stdin = strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"ls"}}`)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit := 0
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	}
	if exit != 2 {
		t.Fatalf("missing spec should block: exit %d, stderr %q", exit, stderr.String())
	}
}

// TestWickGate_TimeoutOnHangingStdin: if stdin never delivers, the
// 3s read timeout fires + binary blocks.
func TestWickGate_TimeoutOnHangingStdin(t *testing.T) {
	bin, _, _, specPath := setupGate(t, []gate.CommandRule{{Pattern: "ls *"}})
	cmd := exec.Command(bin)
	cmd.Env = append([]string{}, "GATE_SPEC="+specPath, "PATH="+pathEnv())
	// Pipe with no writer — Read blocks forever, but binary should
	// time out on its own and exit 2.
	stdinR, stdinW, _ := pipePair()
	cmd.Stdin = stdinR
	defer stdinW.Close()

	start := time.Now()
	err := cmd.Run()
	elapsed := time.Since(start)
	exit := 0
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run: %v", err)
	}
	if exit != 2 {
		t.Errorf("timeout should block: exit %d", exit)
	}
	if elapsed > 5*time.Second {
		t.Errorf("timeout took too long: %v (binary expected ~3s)", elapsed)
	}
}

// readCommands reads commands.jsonl into a slice of gate.Entry.
func readCommands(t *testing.T, layout config.Layout, sessionID string) []gate.Entry {
	t.Helper()
	var out []gate.Entry
	err := storage.ReadJSONL(layout.SessionCommands(sessionID), func(line []byte) bool {
		var e gate.Entry
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// terminalOnly drops audit-trail "stage=..." entries so tests can
// assert just the user-visible decision row. The gate binary emits one
// terminal entry (Status=allowed|blocked, no Stage) per invocation.
func terminalOnly(entries []gate.Entry) []gate.Entry {
	out := make([]gate.Entry, 0, len(entries))
	for _, e := range entries {
		if e.Stage == "" && e.Status != "" {
			out = append(out, e)
		}
	}
	return out
}
