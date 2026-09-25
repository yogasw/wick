package codex

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// memoryArgs is the shape the ai-memory backend contributes for codex: `-c`
// TOML overrides registering the MCP server.
var memoryArgs = []string{
	"-c", `mcp_servers.ai-memory.url="http://127.0.0.1:49374/mcp"`,
	"-c", `mcp_servers.ai-memory.default_tools_approval_mode="approve"`,
}

// stubBin writes a tiny executable so Spawn gets past cmd.Start() and returns
// a Process whose Argv we can read. A non-existent binary is not usable: Spawn
// returns (nil, err) on a failed start and the test would assert nothing.
func stubBin(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub spawn needs a POSIX shell script")
	}
	p := filepath.Join(t.TempDir(), "codex-stub")
	if err := os.WriteFile(p, []byte("#!/bin/sh\ncat >/dev/null\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func indexOfValue(argv []string, v string) int {
	for i, a := range argv {
		if a == v {
			return i
		}
	}
	return -1
}

// TestSpawnerArgvMemoryBeforePrompt is the argv-order lock for Agent Memory on
// codex. `codex exec` takes its PROMPT as a positional operand appended at the
// very end (with `resume <id>` just before it), so every memory `-c` pair must
// land ahead of both — a value-carrying flag after the prompt would either eat
// the prompt or be parsed as part of it.
func TestSpawnerArgvMemoryBeforePrompt(t *testing.T) {
	provider.SetMemorySpawn(func(*provider.Instance, provider.Type, string) (provider.MemoryContribution, error) {
		return provider.MemoryContribution{
			Args: append([]string{}, memoryArgs...),
			Env:  []string{"AI_MEMORY_SERVER_URL=http://127.0.0.1:49374"},
		}, nil
	})
	t.Cleanup(func() { provider.SetMemorySpawn(nil) })

	restore := homeDir
	homeDir = func() (string, error) { return t.TempDir(), nil }
	t.Cleanup(func() { homeDir = restore })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := Spawner{Binary: stubBin(t)}.Spawn(ctx, provider.SpawnOptions{
		Workspace:      t.TempDir(),
		Instance:       &provider.Instance{Type: provider.TypeCodex, UseAgentMemory: true},
		ResumeID:       "thread-42",
		InitialMessage: "halo codex",
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Cleanup(func() { _ = p.Kill() })
	argv := p.Argv()

	if last := argv[len(argv)-1]; last != "halo codex" {
		t.Fatalf("prompt is not the argv tail, last = %q: %v", last, argv)
	}
	resumeAt := indexOfValue(argv, "resume")
	if resumeAt < 0 {
		t.Fatalf("resume subcommand missing: %v", argv)
	}
	for _, want := range memoryArgs {
		at := indexOfValue(argv, want)
		if at < 0 {
			t.Fatalf("memory override %q missing from argv: %v", want, argv)
		}
		if at >= resumeAt {
			t.Fatalf("memory override %q at %d lands at/after `resume` (%d): %v", want, at, resumeAt, argv)
		}
	}
	// Both pairs stay intact: each value is still preceded by its own -c.
	for i := 1; i < len(memoryArgs); i += 2 {
		at := indexOfValue(argv, memoryArgs[i])
		if argv[at-1] != "-c" {
			t.Fatalf("memory value %q lost its -c, preceded by %q: %v", memoryArgs[i], argv[at-1], argv)
		}
	}
}

// TestSpawnerArgvNoMemoryWhenOff is the isolation guard: an instance with the
// toggle off must spawn exactly as it did before the feature existed.
func TestSpawnerArgvNoMemoryWhenOff(t *testing.T) {
	provider.SetMemorySpawn(func(*provider.Instance, provider.Type, string) (provider.MemoryContribution, error) {
		t.Fatal("memory hook must not be consulted for an instance with the toggle off")
		return provider.MemoryContribution{}, nil
	})
	t.Cleanup(func() { provider.SetMemorySpawn(nil) })

	restore := homeDir
	homeDir = func() (string, error) { return t.TempDir(), nil }
	t.Cleanup(func() { homeDir = restore })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := Spawner{Binary: stubBin(t)}.Spawn(ctx, provider.SpawnOptions{
		Workspace: t.TempDir(),
		Instance:  &provider.Instance{Type: provider.TypeCodex},
	})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Cleanup(func() { _ = p.Kill() })
	if joined := strings.Join(p.Argv(), " "); strings.Contains(joined, "mcp_servers.ai-memory") {
		t.Fatalf("memory args leaked into a spawn with the toggle off: %s", joined)
	}
}
