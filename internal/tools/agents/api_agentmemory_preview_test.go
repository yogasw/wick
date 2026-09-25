package agents

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/agentmemory"
	_ "github.com/yogasw/wick/internal/agents/agentmemory/aimemory"
	"github.com/yogasw/wick/internal/agents/provider"
)

// TestAgentMemoryConfigPreview covers the API-side twin of the router preview:
// it must resolve with the toggle FORCED ON (so the user can see the effect
// before enabling it), render env one per line, and fold codex `-c key=val`
// pairs onto a single line each.
func TestAgentMemoryConfigPreview(t *testing.T) {
	var sawToggle bool
	provider.SetMemorySpawn(func(ins *provider.Instance, _ provider.Type, _ string) (provider.MemoryContribution, error) {
		sawToggle = ins.UseAgentMemory
		return provider.MemoryContribution{
			Env:  []string{"AI_MEMORY_SERVER_URL=http://127.0.0.1:49374"},
			Args: []string{"-c", `mcp_servers.ai-memory.url="http://127.0.0.1:49374/mcp"`},
		}, nil
	})
	t.Cleanup(func() { provider.SetMemorySpawn(nil) })

	// Toggle saved OFF — the preview must still resolve.
	got := agentMemoryConfigPreview(provider.Instance{Type: provider.TypeCodex})
	if !sawToggle {
		t.Fatal("preview must force the toggle on before resolving")
	}
	want := "AI_MEMORY_SERVER_URL=http://127.0.0.1:49374\n" +
		`-c mcp_servers.ai-memory.url="http://127.0.0.1:49374/mcp"`
	if got != want {
		t.Fatalf("preview\n  got:  %q\n  want: %q", got, want)
	}
}

// TestAgentMemoryConfigPreviewUnresolvedIsEmpty: a backend that cannot resolve
// renders nothing rather than a half-built command.
func TestAgentMemoryConfigPreviewUnresolvedIsEmpty(t *testing.T) {
	provider.SetMemorySpawn(func(*provider.Instance, provider.Type, string) (provider.MemoryContribution, error) {
		return provider.MemoryContribution{}, errUnresolved{}
	})
	t.Cleanup(func() { provider.SetMemorySpawn(nil) })

	if got := agentMemoryConfigPreview(provider.Instance{Type: provider.TypeClaude}); got != "" {
		t.Fatalf("unresolved backend should preview empty, got %q", got)
	}
}

type errUnresolved struct{}

func (errUnresolved) Error() string { return "unknown backend" }

// TestSpawnContribPreviewRendering pins the shared renderer both previews use:
// non `-c` tokens stay on their own line, and a trailing bare `-c` is not
// allowed to read past the end of the slice.
func TestSpawnContribPreviewRendering(t *testing.T) {
	got := spawnContribPreview(
		[]string{"A=1", "B=2"},
		[]string{"--mcp-config", "{}", "-c"},
	)
	want := strings.Join([]string{"A=1", "B=2", "--mcp-config", "{}", "-c"}, "\n")
	if got != want {
		t.Fatalf("render\n  got:  %q\n  want: %q", got, want)
	}
}

// TestCodexReportsCaptureSupported: the panel's "recording is not wired for
// …" note is derived from the spawn contribution, not from a list of types,
// so wiring codex's hooks has to make the note disappear and the switch
// become usable — with nobody editing copy. This drives the REAL backend
// registry rather than a stub, because what is being checked is exactly that
// the real contribution differs when capture is on.
func TestCodexReportsCaptureSupported(t *testing.T) {
	agentmemory.Init()
	t.Cleanup(func() { provider.SetMemorySpawn(nil) })

	if _, ok := agentmemory.Get("ai-memory"); !ok {
		t.Skip("ai-memory backend is not registered in this binary")
	}
	// The hooks name the binary, so the test gives wick one: a stub in
	// wick's own bin dir, which is where an installed backend lives. A test
	// that skipped on a host without ai-memory would prove nothing in the
	// gate, which is where this claim has to hold.
	withStubBackendBin(t)

	for _, tp := range []provider.Type{provider.TypeCodex, provider.TypeClaude} {
		ins := provider.Instance{Type: tp, AgentMemoryProvider: "ai-memory"}
		supported, captureOK := agentMemorySupport(ins)
		if !supported {
			t.Fatalf("%s: memory is not supported at all", tp)
		}
		if !captureOK {
			t.Fatalf("%s: capture reports unsupported, so the panel still tells people it cannot record", tp)
		}
		if note := agentMemoryCaptureNote(ins, supported, captureOK); note != "" {
			t.Fatalf("%s: a capture note survives after capture was wired: %q", tp, note)
		}
	}
}

// TestCodexPreviewShowsTheHooksWhenCaptureIsOn: the "what wick passes" block
// is where an operator reads what will run on every tool call, so the hook
// overrides belong in it — and only when capture is actually on.
func TestCodexPreviewShowsTheHooksWhenCaptureIsOn(t *testing.T) {
	agentmemory.Init()
	t.Cleanup(func() { provider.SetMemorySpawn(nil) })

	if _, ok := agentmemory.Get("ai-memory"); !ok {
		t.Skip("ai-memory backend is not registered in this binary")
	}
	withStubBackendBin(t)

	on := agentMemoryConfigPreview(provider.Instance{Type: provider.TypeCodex, AgentMemoryProvider: "ai-memory", AgentMemoryCapture: true})
	if !strings.Contains(on, "hooks.SessionStart") {
		t.Fatalf("capture-on preview hides the hooks:\n%s", on)
	}
	if !strings.Contains(on, "--dangerously-bypass-hook-trust") {
		t.Fatalf("the preview hides the flag that makes the hooks run:\n%s", on)
	}

	off := agentMemoryConfigPreview(provider.Instance{Type: provider.TypeCodex, AgentMemoryProvider: "ai-memory"})
	if strings.Contains(off, "hooks.SessionStart") {
		t.Fatalf("capture-off preview shows a spawn that will not happen:\n%s", off)
	}
}

// withStubBackendBin puts an executable where wick keeps an installed
// backend, so BinPath resolves without anything being installed on the host.
func withStubBackendBin(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	name := "ai-memory"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	prev := agentmemory.BinDir()
	t.Cleanup(func() { agentmemory.SetBinDir(prev) })
	agentmemory.SetBinDir(dir)
}
