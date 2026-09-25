package claude

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

// memoryArgs is the shape the ai-memory backend contributes for claude: a
// variadic --mcp-config carrying an inline JSON value, closed by the boolean
// --strict-mcp-config.
var memoryArgs = []string{
	"--mcp-config", `{"mcpServers":{"ai-memory":{"type":"http","url":"http://127.0.0.1:49374/mcp"}}}`,
	"--strict-mcp-config",
}

// stubBin writes a tiny executable that just drains stdin, so Spawn gets past
// cmd.Start() and hands back a Process whose Argv we can read. A non-existent
// binary is NOT usable here: Spawn returns (nil, err) on a failed start, and a
// test that then skips asserts nothing at all.
func stubBin(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub spawn needs a POSIX shell script")
	}
	p := filepath.Join(t.TempDir(), "claude-stub")
	if err := os.WriteFile(p, []byte("#!/bin/sh\ncat >/dev/null\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// spawnArgv runs one spawn against the stub and returns its argv.
func spawnArgv(t *testing.T, opt provider.SpawnOptions) []string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p, err := Spawner{Binary: stubBin(t)}.Spawn(ctx, opt)
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	t.Cleanup(func() { _ = p.Kill() })
	return p.Argv()
}

func indexOfValue(argv []string, v string) int {
	for i, a := range argv {
		if a == v {
			return i
		}
	}
	return -1
}

// TestSpawnArgvMemoryOrder is the argv-order lock for Agent Memory on claude.
//
// claude's --mcp-config is VARIADIC: it keeps consuming the operands that
// follow it. So the memory contribution must never sit at the tail of argv,
// and must never be followed by a bare (non-flag) operand — either mistake
// feeds a prompt or a path into the MCP config list instead of to claude.
func TestSpawnArgvMemoryOrder(t *testing.T) {
	provider.SetMemorySpawn(func(*provider.Instance, provider.Type, string) (provider.MemoryContribution, error) {
		return provider.MemoryContribution{
			Args: append([]string{}, memoryArgs...),
			Env:  []string{"AI_MEMORY_SERVER_URL=http://127.0.0.1:49374"},
		}, nil
	})
	t.Cleanup(func() { provider.SetMemorySpawn(nil) })

	argv := spawnArgv(t, provider.SpawnOptions{
		Workspace: t.TempDir(),
		Instance:  &provider.Instance{Type: provider.TypeClaude, UseAgentMemory: true},
		ResumeID:  "sess-abc",
		MaxTurns:  3,
	})

	cfgAt := indexOfValue(argv, memoryArgs[1])
	if cfgAt < 0 {
		t.Fatalf("memory --mcp-config value missing from argv: %v", argv)
	}
	if argv[cfgAt-1] != "--mcp-config" {
		t.Fatalf("memory config value is not attached to --mcp-config: %v", argv)
	}
	// The variadic flag must be closed by a boolean flag, never trail off.
	if argv[cfgAt+1] != "--strict-mcp-config" {
		t.Fatalf("memory config value is not closed by --strict-mcp-config, next = %q", argv[cfgAt+1])
	}
	// Nothing the contribution could swallow may follow it as a bare operand.
	for i := cfgAt + 2; i < len(argv); i++ {
		if strings.HasPrefix(argv[i], "-") {
			break
		}
		t.Fatalf("bare operand %q follows the variadic --mcp-config at %d: %v", argv[i], i, argv)
	}
	// And the trailing session flags must still come after it — the insertion
	// point is mid-argv, not appended at the end.
	if at := indexOfValue(argv, "--resume"); at < cfgAt {
		t.Fatalf("--resume at %d precedes the memory args at %d: %v", at, cfgAt, argv)
	}
	if last := argv[len(argv)-1]; last == memoryArgs[1] {
		t.Fatalf("memory config value is the argv tail: %v", argv)
	}
}

// TestSpawnArgvNoMemoryWhenOff is the isolation guard: an instance that does
// not use Agent Memory must spawn byte-identically to before the feature.
func TestSpawnArgvNoMemoryWhenOff(t *testing.T) {
	provider.SetMemorySpawn(func(*provider.Instance, provider.Type, string) (provider.MemoryContribution, error) {
		t.Fatal("memory hook must not be consulted for an instance with the toggle off")
		return provider.MemoryContribution{}, nil
	})
	t.Cleanup(func() { provider.SetMemorySpawn(nil) })

	argv := spawnArgv(t, provider.SpawnOptions{
		Workspace: t.TempDir(),
		Instance:  &provider.Instance{Type: provider.TypeClaude},
	})
	if strings.Contains(strings.Join(argv, " "), "--strict-mcp-config") {
		t.Fatalf("memory args leaked into a spawn with the toggle off: %v", argv)
	}
}
