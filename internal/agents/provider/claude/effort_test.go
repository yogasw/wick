package claude

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

func TestSpawnEnvEffortDropsInstanceLevel(t *testing.T) {
	env := spawnEnv(nil, provider.SpawnOptions{ExtraEnv: []string{"CLAUDE_CODE_EFFORT_LEVEL=low", "FOO=1"}, Effort: "max"})
	for _, e := range env {
		if strings.HasPrefix(e, "CLAUDE_CODE_EFFORT_LEVEL=") {
			t.Fatalf("a picked effort must drop the instance env, env=%q", env)
		}
	}
	if !slices.Contains(env, "FOO=1") {
		t.Fatalf("other env must stay, env=%q", env)
	}
	kept := spawnEnv(nil, provider.SpawnOptions{ExtraEnv: []string{"CLAUDE_CODE_EFFORT_LEVEL=low"}})
	if !slices.Contains(kept, "CLAUDE_CODE_EFFORT_LEVEL=low") {
		t.Fatalf("no pick keeps the instance env, env=%q", kept)
	}
}

func TestEffortArgs(t *testing.T) {
	if got := effortArgs("max", []string{"--model", "x"}); !slices.Equal(got, []string{"--effort", "max"}) {
		t.Fatalf("got %q", got)
	}
	if got := effortArgs("", nil); got != nil {
		t.Fatalf("no pick, no flag: %q", got)
	}
	if got := effortArgs("high", []string{"--effort=low"}); got != nil {
		t.Fatalf("instance args already pass one: %q", got)
	}
}

func TestClaudeEffortResolverRegistered(t *testing.T) {
	caps, registered, err := provider.EffortCapabilitiesFor(context.Background(), provider.Instance{Type: provider.TypeClaude, Name: "acme"}, "")
	if err != nil || !registered {
		t.Fatalf("registered=%v err=%v", registered, err)
	}
	var ids []string
	for _, o := range caps.Options {
		ids = append(ids, o.ID)
	}
	if !slices.Equal(ids, []string{"low", "medium", "high", "xhigh", "max"}) {
		t.Fatalf("options %v", ids)
	}
	if caps.Default != "" {
		t.Fatalf("no instance level, no default claimed: %q", caps.Default)
	}
}

func TestClaudeEffortDefaultFromInstanceEnv(t *testing.T) {
	ins := provider.Instance{Type: provider.TypeClaude, Name: "acme-env", Env: []string{"CLAUDE_CODE_EFFORT_LEVEL=high"}}
	caps, _, err := provider.EffortCapabilitiesFor(context.Background(), ins, "")
	if err != nil || caps.Default != "high" {
		t.Fatalf("default %q err=%v", caps.Default, err)
	}
}
