package gate_test

import (
	"context"
	"encoding/json"
	"os"
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

// writeTestWickYML drops a minimal wick.yml in a fresh tempdir and
// chdirs the test process into it. Both the test process and any
// gate child proc it spawns now resolve appname via that file —
// cwd-inherited, no env or ldflag wiring.
func writeTestWickYML(t *testing.T, app string) {
	t.Helper()
	dir := t.TempDir()
	yml := filepath.Join(dir, "wick.yml")
	if err := os.WriteFile(yml, []byte("name: "+app+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
}

// buildGate compiles cmd/gate into a temp file. AppName comes from
// wick.yml in the test process's cwd (see writeTestWickYML), so
// binary name is irrelevant. Skips when `go build` is unavailable.
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

// setupGate isolates HOME to a tempdir, writes the shared spec
// (rules + auto-approved), and returns the gate binary + the appName
// the test should pass to LoadSpec lookups.
func setupGate(t *testing.T, rules []gate.CommandRule) (bin, app string, layout config.Layout) {
	t.Helper()
	app = "itest"

	// Build BEFORE swapping HOME — otherwise `go build` populates
	// $TempDir/go/pkg/mod with read-only module-cache files, and
	// t.TempDir cleanup fails with "permission denied" on Linux.
	bin = buildGate(t)

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	writeTestWickYML(t, app)
	layout = config.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID:     "S1",
		Origin: session.OriginUI,
	}); err != nil {
		t.Fatal(err)
	}
	if err := gate.WriteSharedSpec(app, gate.Spec{Rules: rules}); err != nil {
		t.Fatal(err)
	}
	return bin, app, layout
}

// runGate invokes the gate binary with the given stdin. The binary
// derives all paths from gate.AppName() (its own filename) + the HOME
// env var inherited from the test process — no env vars, no ldflags.
func runGate(t *testing.T, bin, stdin string) (int, string) {
	t.Helper()
	cmd := exec.Command(bin)
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

// TestGate_Allow: a "ls *" rule lets `ls -la` through.
func TestGate_Allow(t *testing.T) {
	bin, app, _ := setupGate(t, []gate.CommandRule{{Pattern: "ls *"}})
	exit, _ := runGate(t, bin,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls -la"}}`)
	if exit != 0 {
		t.Fatalf("exit: got %d, want 0 (allow)", exit)
	}
	entries := terminalOnly(readSharedCommands(t, app))
	if len(entries) != 1 || entries[0].Status != "allowed" || entries[0].Cmd != "ls -la" {
		t.Fatalf("commands.jsonl: %+v", entries)
	}
}

// TestGate_BlockUnlistedCommand: `rm -rf .` is not whitelisted →
// gate dials the shared socket; with no daemon listening, fail-OPEN
// kicks in (allow) so non-wick Claude sessions don't break. Terminal
// entry is "allowed" with reason "no_socket".
func TestGate_BlockUnlistedCommand(t *testing.T) {
	bin, app, _ := setupGate(t, []gate.CommandRule{{Pattern: "ls *"}})
	exit, _ := runGate(t, bin,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"rm -rf ."}}`)
	if exit != 0 {
		t.Fatalf("exit: got %d, want 0 (fail-open allow)", exit)
	}
	entries := terminalOnly(readSharedCommands(t, app))
	if len(entries) != 1 || entries[0].Status != "allowed" || entries[0].Cmd != "rm -rf ." {
		t.Fatalf("commands.jsonl: %+v", entries)
	}
	if entries[0].Decision != "no_socket" {
		t.Errorf("expected decision no_socket, got %q", entries[0].Decision)
	}
}

// TestGate_BlockShellMetacharOnAllowedRule: even a "git *" match
// must not let a piped command through. Matcher returns false → fall
// through to socket dial → no daemon → fail-OPEN allow with reason
// "no_socket". A terminal allowed row with that reason confirms the
// metachar bypassed the rule and went through the dial path.
func TestGate_BlockShellMetacharOnAllowedRule(t *testing.T) {
	bin, app, _ := setupGate(t, []gate.CommandRule{{Pattern: "git *"}})
	exit, _ := runGate(t, bin,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"git status; rm -rf ."}}`)
	if exit != 0 {
		t.Fatalf("fail-open allow expected: exit %d", exit)
	}
	entries := terminalOnly(readSharedCommands(t, app))
	if len(entries) == 0 {
		t.Fatalf("expected at least one terminal entry, got none")
	}
	last := entries[len(entries)-1]
	if last.Status != "allowed" || last.Decision != "no_socket" {
		t.Errorf("expected allowed/no_socket, got status=%q decision=%q", last.Status, last.Decision)
	}
}

// TestGate_AuditTrailLogged: every invocation must emit a
// "received" stage + a terminal status row, tied by the same
// RequestID.
func TestGate_AuditTrailLogged(t *testing.T) {
	bin, app, _ := setupGate(t, []gate.CommandRule{{Pattern: "ls *"}})
	exit, _ := runGate(t, bin,
		`{"hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"ls -la"}}`)
	if exit != 0 {
		t.Fatalf("exit: got %d", exit)
	}

	all := readSharedCommands(t, app)
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

// TestGate_MalformedStdin: garbage input fails safe (block).
func TestGate_MalformedStdin(t *testing.T) {
	bin, _, _ := setupGate(t, []gate.CommandRule{{Pattern: "ls *"}})
	exit, _ := runGate(t, bin, "not json")
	if exit != 2 {
		t.Fatalf("malformed should block: exit %d", exit)
	}
}

// TestGate_MissingSharedSpecIsEmpty: no spec file → empty rules →
// every command falls through to the socket dial → no daemon →
// fail-OPEN allow. Confirms LoadSpec doesn't panic on missing file
// and the no-socket path returns exit 0.
func TestGate_MissingSharedSpecIsEmpty(t *testing.T) {
	bin := buildGate(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	writeTestWickYML(t, "itest-empty")

	cmd := exec.Command(bin)
	cmd.Stdin = strings.NewReader(`{"tool_name":"Bash","tool_input":{"command":"ls"}}`)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit := 0
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	}
	if exit != 0 {
		t.Fatalf("missing spec fail-open expected: exit %d, stderr %q", exit, stderr.String())
	}
}

// TestGate_TimeoutOnHangingStdin: if stdin never delivers, the 3s
// read timeout fires + binary blocks.
func TestGate_TimeoutOnHangingStdin(t *testing.T) {
	bin, _, _ := setupGate(t, []gate.CommandRule{{Pattern: "ls *"}})
	cmd := exec.Command(bin)
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

// readSharedCommands reads the shared commands.jsonl for appName
// into a slice of gate.Entry.
func readSharedCommands(t *testing.T, app string) []gate.Entry {
	t.Helper()
	var out []gate.Entry
	err := storage.ReadJSONL(gate.SharedCommandsPath(app), func(line []byte) bool {
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
// assert just the user-visible decision row.
func terminalOnly(entries []gate.Entry) []gate.Entry {
	out := make([]gate.Entry, 0, len(entries))
	for _, e := range entries {
		if e.Stage == "" && e.Status != "" {
			out = append(out, e)
		}
	}
	return out
}
