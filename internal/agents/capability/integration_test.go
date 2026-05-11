//go:build integration

// Integration tests for the capability probe flow end-to-end. Builds a
// tiny fake provider binary on-the-fly so the test exercises the real
// Writer → spawn → Prober → sentinel-check path without needing
// claude / codex / gemini installed on the CI runner.
//
// Run with: go test -tags=integration ./internal/agents/capability/...
//
// Why a build tag: these tests spawn `go build` and a subprocess; they
// take seconds, not microseconds. Tagging them out of the default
// `go test ./...` keeps the fast unit run fast.
package capability

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// fakeProviderSource is the source of a tiny provider binary built per
// test. The fake reads a single arg <sentinel-path>, "consults its
// hook" by exec'ing the installed gate binary (which the writer also
// pointed at), and — if the hook says allow — creates the sentinel
// file. If the hook says deny (exit 0 + deny JSON, or non-zero exit),
// the fake skips the sentinel.
//
// This mirrors the real provider lifecycle: the provider sees the deny
// envelope and refuses to run the tool, which is exactly what the
// capability probe is verifying.
const fakeProviderSource = `package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: fakeprov <hook-cmd> <sentinel-path>")
		os.Exit(1)
	}
	hookCmd := os.Args[1]
	sentinel := os.Args[2]

	// Split the hook command into argv: "gate-binary --probe-deny --provider=foo"
	parts := strings.Fields(hookCmd)
	if len(parts) == 0 {
		fmt.Fprintln(os.Stderr, "empty hook command")
		os.Exit(1)
	}

	// Build a minimal PreToolUse-shaped payload and feed it to the hook.
	payload := map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"cwd":             ".",
		"tool_input":      map[string]string{"command": "touch sentinel"},
	}
	data, _ := json.Marshal(payload)

	cmd := exec.Command(parts[0], parts[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, "stdin pipe:", err)
		os.Exit(1)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, "stdout pipe:", err)
		os.Exit(1)
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "start hook:", err)
		os.Exit(1)
	}
	_, _ = stdin.Write(data)
	_ = stdin.Close()
	out, _ := io.ReadAll(stdout)
	_ = cmd.Wait()

	// Parse the hook's stdout. If it contains "deny" anywhere in the
	// JSON, the fake provider obeys and exits without touching the
	// sentinel. Otherwise (empty stdout = allow fall-through, or
	// allow envelope), it creates the sentinel.
	if strings.Contains(string(out), "\"deny\"") {
		os.Exit(0)
	}
	_ = os.WriteFile(sentinel, []byte("ran"), 0644)
}
`

// buildFakeProvider compiles fakeProviderSource into a temp binary
// the test can spawn. Returns the absolute path. Skips the test if
// `go` is not on PATH (CI runners that don't have a Go toolchain
// shouldn't be running integration tests anyway, but be polite).
func buildFakeProvider(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("integration test needs `go` on PATH to build the fake provider")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(fakeProviderSource), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "fakeprov")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, src)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake provider: %v\n%s", err, out)
	}
	return bin
}

// buildGateBinary compiles cmd/gate into a test-local binary so the
// fake provider can invoke it as its hook. The capability probe path
// uses `--probe-deny --provider=<name>` which doesn't dial the daemon
// socket — pure adapter test.
func buildGateBinary(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("needs `go` on PATH")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "gate")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", bin, "github.com/yogasw/wick/cmd/gate")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build gate: %v\n%s", err, out)
	}
	return bin
}

// fakeWriterIntegration installs the hook by writing a sentinel JSON
// to the workspace and pointing the fake provider at it via env var.
// In production, real Writers install into provider-specific config
// directories — for the integration test we just stash the hook
// command in a file the fake provider reads.
type fakeWriterIntegration struct{}

func (fakeWriterIntegration) Write(workspace, gateBin string) error {
	return os.WriteFile(filepath.Join(workspace, "hook.txt"), []byte(gateBin), 0o644)
}
func (fakeWriterIntegration) Remove(workspace string) error {
	return os.Remove(filepath.Join(workspace, "hook.txt"))
}
func (fakeWriterIntegration) DryRun(workspace, gateBin string) (string, []byte, error) {
	return filepath.Join(workspace, "hook.txt"), []byte(gateBin), nil
}

// fakeProberIntegration spawns the prebuilt fake provider binary,
// passing the hook command (read back from the workspace) and the
// sentinel path. The fake provider then mimics the real "consult hook,
// honor result" loop.
type fakeProberIntegration struct {
	fakeBinary string
}

func (p fakeProberIntegration) SendSentinel(ctx context.Context, workspace, sentinel string) error {
	hookBytes, err := os.ReadFile(filepath.Join(workspace, "hook.txt"))
	if err != nil {
		return fmt.Errorf("read hook: %w", err)
	}
	cmd := exec.CommandContext(ctx, p.fakeBinary, string(hookBytes), sentinel)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("fakeprov: %v\n%s", err, out)
	}
	return nil
}

func TestIntegration_HookHonored(t *testing.T) {
	resetAll()

	gateBin := buildGateBinary(t)
	fakeBin := buildFakeProvider(t)

	Register("fake", Capability{HookSupported: true, InterceptScope: "shell"})
	RegisterHookConfigWriter("fake", fakeWriterIntegration{})
	RegisterProber("fake", fakeProberIntegration{fakeBinary: fakeBin})

	res := HookCapabilityCheck(context.Background(), CheckInput{
		ProviderName:  "fake",
		GateBinary:    gateBin,
		WorkspaceRoot: t.TempDir(),
	})

	if !res.HookVerified {
		t.Errorf("expected hook honored (sentinel absent), got HookError=%q", res.HookError)
	}
}

func TestIntegration_FailOpenWhenHookIgnored(t *testing.T) {
	// Simulate a "broken" provider by pointing the fake at /bin/true
	// (or windows equivalent) — a command that exits 0 with no stdout.
	// The fake will see empty hook output, treat that as allow, and
	// touch the sentinel. HookCapabilityCheck should flag verified=false.
	resetAll()
	fakeBin := buildFakeProvider(t)

	Register("fake", Capability{HookSupported: true, InterceptScope: "shell"})
	RegisterHookConfigWriter("fake", fakeWriterIntegration{})
	// Use a no-op binary as the "hook" — exits cleanly without deny.
	noop := "/bin/true"
	if runtime.GOOS == "windows" {
		// On Windows, `cmd /c exit 0` is a clean no-op.
		// But pass an absolute path the fake can split on whitespace —
		// "cmd /c exit 0" already works as a space-separated argv.
		noop = "cmd /c exit 0"
	}

	// Manually plug the noop "hook" into the writer.
	writerNoop := struct{ fakeWriterIntegration }{}
	RegisterHookConfigWriter("fake", writerOverride{cmd: noop})
	RegisterProber("fake", fakeProberIntegration{fakeBinary: fakeBin})
	_ = writerNoop

	res := HookCapabilityCheck(context.Background(), CheckInput{
		ProviderName:  "fake",
		GateBinary:    noop,
		WorkspaceRoot: t.TempDir(),
	})

	if res.HookVerified {
		t.Errorf("expected verified=false (sentinel created), HookError=%q", res.HookError)
	}
}

// writerOverride lets the fail-open test inject a custom hook command
// (the no-op binary) instead of the real gate path.
type writerOverride struct {
	cmd string
}

func (w writerOverride) Write(workspace, _ string) error {
	return os.WriteFile(filepath.Join(workspace, "hook.txt"), []byte(w.cmd), 0o644)
}
func (w writerOverride) Remove(workspace string) error {
	return os.Remove(filepath.Join(workspace, "hook.txt"))
}
func (w writerOverride) DryRun(workspace, _ string) (string, []byte, error) {
	return filepath.Join(workspace, "hook.txt"), []byte(w.cmd), nil
}

func TestIntegration_ConcurrentChecks(t *testing.T) {
	// Five parallel capability checks share the registries — make sure
	// no race / state corruption with -race detector.
	resetAll()
	gateBin := buildGateBinary(t)
	fakeBin := buildFakeProvider(t)

	Register("fake", Capability{HookSupported: true, InterceptScope: "shell"})
	RegisterHookConfigWriter("fake", fakeWriterIntegration{})
	RegisterProber("fake", fakeProberIntegration{fakeBinary: fakeBin})

	var wg sync.WaitGroup
	root := t.TempDir()
	results := make([]CheckResult, 5)
	for i := 0; i < 5; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = HookCapabilityCheck(context.Background(), CheckInput{
				ProviderName:  "fake",
				GateBinary:    gateBin,
				WorkspaceRoot: root,
			})
		}()
	}
	wg.Wait()

	for i, res := range results {
		if !res.HookVerified {
			t.Errorf("run %d failed: HookError=%q", i, res.HookError)
		}
	}
}

func TestIntegration_GateAdapterShapeCodex(t *testing.T) {
	// Build gate and verify --probe-deny --provider=codex emits the
	// codex-shaped deny envelope. Sanity check that the adapter
	// dispatch wires up at the binary level too.
	gateBin := buildGateBinary(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, gateBin, "--probe-deny", "--provider=codex")
	cmd.Stdin = nil // gate drains stdin then emits
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run gate probe-deny: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("parse gate output %q: %v", out, err)
	}
	if got["permissionDecision"] != "deny" {
		t.Errorf("codex shape missing flat permissionDecision: %+v", got)
	}
	if _, has := got["hookSpecificOutput"]; has {
		t.Error("codex shape should NOT have hookSpecificOutput envelope")
	}
}
