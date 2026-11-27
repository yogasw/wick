package claude

import (
	"context"
	"slices"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

func TestSpawnEnvEffort(t *testing.T) {
	env := spawnEnv(nil, provider.SpawnOptions{ExtraEnv: []string{"CLAUDE_CODE_EFFORT_LEVEL=low"}, Effort: "xhigh"})
	// the pick comes after the instance env, so exec keeps it
	if env[len(env)-1] != "CLAUDE_CODE_EFFORT_LEVEL=xhigh" {
		t.Fatalf("effort must be the last entry, env=%q", env)
	}
	for _, e := range spawnEnv(nil, provider.SpawnOptions{}) {
		if e == "CLAUDE_CODE_EFFORT_LEVEL=" || len(e) > 24 && e[:24] == "CLAUDE_CODE_EFFORT_LEVEL" {
			t.Fatalf("no effort must not set the env, got %q", e)
		}
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
	if !slices.Equal(ids, []string{"low", "medium", "high", "xhigh"}) {
		t.Fatalf("options %v", ids)
	}
	if caps.Options[3].Label != "Extra High" {
		t.Fatalf("label %q", caps.Options[3].Label)
	}
}
