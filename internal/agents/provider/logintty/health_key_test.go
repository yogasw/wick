package logintty

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

func TestHealthKeyGeminiByEnv(t *testing.T) {
	a := provider.Instance{Type: provider.TypeGemini, Name: "a", Env: []string{"GEMINI_API_KEY=AIza-one"}}
	b := provider.Instance{Type: provider.TypeGemini, Name: "b", Env: []string{"GEMINI_API_KEY=AIza-two"}}
	a2 := provider.Instance{Type: provider.TypeGemini, Name: "a2", Env: []string{"GEMINI_API_KEY=AIza-one", "OTHER=x"}}
	ka, kb, ka2 := HealthKey(a), HealthKey(b), HealthKey(a2)
	if ka == "" || ka == kb {
		t.Fatalf("different api keys must give different keys: %q %q", ka, kb)
	}
	if ka != ka2 {
		t.Fatalf("same api key must share a key: %q %q", ka, ka2)
	}
	if HealthKey(provider.Instance{Type: provider.TypeGemini, Name: "bare"}) != "" {
		t.Fatal("a gemini instance with no account env must fall back to type/name")
	}
	if HealthKey(provider.Instance{Type: provider.TypeWick, Name: "wick"}) != "" {
		t.Fatal("wick must fall back to type/name")
	}
	// Through the provider hook: two bare gemini instances never share.
	x := provider.HealthKey(provider.Instance{Type: provider.TypeGemini, Name: "x"})
	y := provider.HealthKey(provider.Instance{Type: provider.TypeGemini, Name: "y"})
	if x == y {
		t.Fatalf("bare gemini instances merged: %q", x)
	}
}

func TestHealthKeyUsageTypesUseUsageIdentity(t *testing.T) {
	ins := provider.Instance{Type: provider.TypeCodex, Name: "c", Env: []string{"CODEX_HOME=/tmp/wick-test-codex-home"}}
	if got, want := HealthKey(ins), UsageIdentity(ins.Type, provider.AccountEnv(ins)); got != want || got == "" {
		t.Fatalf("HealthKey = %q, want UsageIdentity %q", got, want)
	}
}

// R2: an API-key claude/codex instance is its key's account, not the
// OAuth login in its config dir.
func TestHealthKeyAPIKeyInstances(t *testing.T) {
	dir := t.TempDir()
	oauth := provider.Instance{Type: provider.TypeClaude, Name: "o", Env: []string{"CLAUDE_CONFIG_DIR=" + dir}}
	k1 := provider.Instance{Type: provider.TypeClaude, Name: "k1", Env: []string{"CLAUDE_CONFIG_DIR=" + dir, "ANTHROPIC_API_KEY=sk-ant-one"}}
	k2 := provider.Instance{Type: provider.TypeClaude, Name: "k2", Env: []string{"CLAUDE_CONFIG_DIR=" + dir, "ANTHROPIC_API_KEY=sk-ant-two"}}
	ko, a, b := HealthKey(oauth), HealthKey(k1), HealthKey(k2)
	if !strings.HasPrefix(a, "claude:env:") || a == b || a == ko {
		t.Fatalf("keys oauth=%q k1=%q k2=%q", ko, a, b)
	}
	if strings.Contains(a, "sk-ant") {
		t.Fatal("plaintext key in health key")
	}
	cx := HealthKey(provider.Instance{Type: provider.TypeCodex, Name: "c", Env: []string{"OPENAI_API_KEY=sk-x"}})
	if !strings.HasPrefix(cx, "codex:env:") {
		t.Fatalf("codex api-key key = %q", cx)
	}
}
