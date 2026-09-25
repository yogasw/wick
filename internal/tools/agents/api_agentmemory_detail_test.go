package agents

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

// TestAgentMemorySupportIsMeasured: support is read off the spawn
// contribution, never off a hardcoded type list. A type the backend declines
// (empty args AND empty env) is unsupported; a type whose contribution does
// not change when Capture flips can read memory but cannot record.
func TestAgentMemorySupportIsMeasured(t *testing.T) {
	// claude: memory wires in, and capture adds a --settings pair.
	// codex: memory wires in, capture changes nothing.
	// gemini: nothing at all.
	provider.SetMemorySpawn(func(ins *provider.Instance, tp provider.Type, _ string) (provider.MemoryContribution, error) {
		switch tp {
		case provider.TypeClaude:
			args := []string{"--mcp-config", "{}"}
			if ins.AgentMemoryCapture {
				args = append([]string{"--settings", "{}"}, args...)
			}
			return provider.MemoryContribution{Args: args}, nil
		case provider.TypeCodex:
			return provider.MemoryContribution{Args: []string{"-c", "mcp_servers.ai-memory.url=\"x\""}}, nil
		default:
			return provider.MemoryContribution{}, nil
		}
	})
	t.Cleanup(func() { provider.SetMemorySpawn(nil) })

	for _, tc := range []struct {
		typ             provider.Type
		wantSupported   bool
		wantCaptureable bool
	}{
		{provider.TypeClaude, true, true},
		{provider.TypeCodex, true, false},
		{provider.TypeGemini, false, false},
	} {
		sup, canRecord := agentMemorySupport(provider.Instance{Type: tc.typ})
		if sup != tc.wantSupported || canRecord != tc.wantCaptureable {
			t.Fatalf("%s: supported=%v capture=%v, want %v/%v", tc.typ, sup, canRecord, tc.wantSupported, tc.wantCaptureable)
		}
	}
}

// TestAgentMemorySupportSurvivesMutation guards the copy semantics: the probe
// forces UseAgentMemory/AgentMemoryCapture on to measure, and must not leave
// the caller's instance carrying settings the operator never saved.
func TestAgentMemorySupportSurvivesMutation(t *testing.T) {
	provider.SetMemorySpawn(func(*provider.Instance, provider.Type, string) (provider.MemoryContribution, error) {
		return provider.MemoryContribution{Args: []string{"--mcp-config", "{}"}}, nil
	})
	t.Cleanup(func() { provider.SetMemorySpawn(nil) })

	ins := provider.Instance{Type: provider.TypeClaude}
	agentMemorySupport(ins)
	if ins.UseAgentMemory || ins.AgentMemoryCapture {
		t.Fatalf("probe leaked into the caller's instance: use=%v capture=%v", ins.UseAgentMemory, ins.AgentMemoryCapture)
	}
}

// TestAgentMemoryCaptureNote: silent when recording works or when memory is
// unsupported outright (the whole card is hidden there, and a second sentence
// about capture would just be noise), and phrased as the CURRENT state rather
// than as a permanent verdict when it does not.
func TestAgentMemoryCaptureNote(t *testing.T) {
	if got := agentMemoryCaptureNote(provider.Instance{Type: provider.TypeClaude}, true, true); got != "" {
		t.Fatalf("working capture should say nothing, got %q", got)
	}
	if got := agentMemoryCaptureNote(provider.Instance{Type: provider.TypeGemini}, false, false); got != "" {
		t.Fatalf("unsupported type should say nothing, got %q", got)
	}
	got := agentMemoryCaptureNote(provider.Instance{Type: provider.TypeCodex}, true, false)
	if !strings.Contains(got, "codex") || !strings.Contains(got, "not recorded") {
		t.Fatalf("note should name the type and the consequence, got %q", got)
	}
	if strings.Contains(got, "never") {
		t.Fatalf("note must describe the current state, not a permanent verdict: %q", got)
	}
}

// TestAgentMemoryDetailDTOHidesTheToken is the one rule that cannot regress:
// the stored auth token is replaced by a boolean on its way out.
func TestAgentMemoryDetailDTOHidesTheToken(t *testing.T) {
	provider.SetMemorySpawn(func(*provider.Instance, provider.Type, string) (provider.MemoryContribution, error) {
		return provider.MemoryContribution{Args: []string{"--mcp-config", "{}"}}, nil
	})
	t.Cleanup(func() { provider.SetMemorySpawn(nil) })

	dto := agentMemoryDetailDTO(provider.Instance{
		Type:                 provider.TypeClaude,
		Name:                 "main",
		UseAgentMemory:       true,
		AgentMemoryAuthKey:   "wick_cenc_supersecret",
		AgentMemoryServerURL: "http://10.0.0.5:49374/",
	})
	if !dto.KeySet {
		t.Fatal("KeySet must report the stored token")
	}
	blob := dto.Preview + dto.ServerURL + dto.EffectiveURL + dto.CaptureNote
	if strings.Contains(blob, "supersecret") {
		t.Fatalf("stored token leaked into the payload: %q", blob)
	}
	// The override wins over the managed daemon, trailing slash dropped so
	// the FE can append a path to it.
	if dto.EffectiveURL != "http://10.0.0.5:49374" {
		t.Fatalf("EffectiveURL = %q, want the instance override without its trailing slash", dto.EffectiveURL)
	}
	if len(dto.Backends) == 0 {
		t.Fatal("the picker needs at least the built-in backend")
	}
	for _, b := range dto.Backends {
		if b.ID == "ai-memory" && b.GitHubURL == "" {
			t.Fatal("ai-memory must carry its upstream repo link")
		}
	}
}
