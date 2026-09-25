package agents

import (
	"strings"
	"testing"

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
