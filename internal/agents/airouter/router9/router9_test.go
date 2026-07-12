package router9

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/airouter"
	"github.com/yogasw/wick/internal/agents/provider"
)

func TestRegisteredWithAirouter(t *testing.T) {
	rt, ok := airouter.Get("9router")
	if !ok {
		t.Fatal("9router not registered with airouter")
	}
	if rt.Desc.NpmPackage != "9router" || rt.Desc.PrefPort != 20128 {
		t.Fatalf("descriptor mismatch: %+v", rt.Desc)
	}
}

func TestLaunchArgs(t *testing.T) {
	args, env := launch(25000)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--port 25000") || !strings.Contains(joined, "--host 127.0.0.1") {
		t.Fatalf("launch args missing port/host bind: %v", args)
	}
	if len(env) != 0 {
		t.Fatalf("9router uses flags, not env: %v", env)
	}
}

func TestContributeClaude(t *testing.T) {
	ins := provider.Instance{AIRouterModels: map[string]string{"opus": "cc/opus", "haiku": "cc/haiku"}}
	_, env, err := (hook{}).Contribute(provider.TypeClaude, ins, "http://x/airouter/9router/v1", "sk_9router")
	if err != nil {
		t.Fatal(err)
	}
	j := strings.Join(env, "\n")
	for _, want := range []string{
		"ANTHROPIC_BASE_URL=http://x/airouter/9router/v1",
		"ANTHROPIC_AUTH_TOKEN=sk_9router",
		"ANTHROPIC_DEFAULT_OPUS_MODEL=cc/opus",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL=cc/haiku",
	} {
		if !strings.Contains(j, want) {
			t.Fatalf("claude env missing %q in\n%s", want, j)
		}
	}
	// An unset slot (sonnet) must be omitted so the CLI keeps its own default.
	if strings.Contains(j, "SONNET") {
		t.Fatalf("unset sonnet slot should be omitted: %s", j)
	}
}

func TestContributeCodex(t *testing.T) {
	ins := provider.Instance{AIRouterModels: map[string]string{"model": "gpt-x"}}
	args, env, err := (hook{}).Contribute(provider.TypeCodex, ins, "http://x/airouter/9router/v1", "sk_9router")
	if err != nil {
		t.Fatal(err)
	}
	ja := strings.Join(args, " ")
	if !strings.Contains(ja, "model_provider=9router") {
		t.Fatalf("codex args missing model_provider: %v", args)
	}
	if !strings.Contains(ja, "--model gpt-x") {
		t.Fatalf("codex --model not set: %v", args)
	}
	if strings.Join(env, " ") != "OPENAI_API_KEY=sk_9router" {
		t.Fatalf("codex env = %v", env)
	}
}

func TestSlotsAndDefaultKey(t *testing.T) {
	if (hook{}).DefaultKey() != "sk_9router" {
		t.Fatal("default key")
	}
	if len((hook{}).Slots(provider.TypeClaude)) != 3 {
		t.Fatal("claude should expose 3 tiers")
	}
	if (hook{}).Slots(provider.TypeGemini) != nil {
		t.Fatal("gemini unsupported → nil slots")
	}
}
