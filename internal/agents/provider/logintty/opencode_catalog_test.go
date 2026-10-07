package logintty

import (
	"context"
	"slices"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

// withCatalog warms the provider catalog cache for a fresh instance with
// cat (nil + err = a failed fetch) and returns that instance.
func withCatalog(t *testing.T, cat *provider.OpencodeCatalog, err error) provider.Instance {
	t.Helper()
	ins := provider.Instance{Type: provider.TypeOpencode, Name: "oc", OpencodeConfig: &provider.OpencodeConfig{DataDir: t.TempDir()}}
	prev := provider.OpencodeCatalogFetcher
	provider.OpencodeCatalogFetcher = func(context.Context, provider.Instance) (*provider.OpencodeCatalog, error) { return cat, err }
	t.Cleanup(func() { provider.OpencodeCatalogFetcher = prev })
	_, _ = provider.FetchOpencodeCatalog(context.Background(), ins)
	return ins
}

func sampleCatalog() *provider.OpencodeCatalog {
	return &provider.OpencodeCatalog{
		Providers: []provider.OpencodeCatalogProvider{
			{ID: "openai", Name: "OpenAI", Env: []string{"OPENAI_API_KEY"}},
			{ID: "github-copilot", Name: "GitHub Copilot"},
			{ID: "anthropic", Name: "Anthropic", Env: []string{"ANTHROPIC_API_KEY"}},
			{ID: "zai", Name: "Z.AI", Env: []string{"ZHIPU_API_KEY", "ZAI_API_KEY"}},
		},
		Auth: map[string][]provider.OpencodeAuthMethod{
			"openai": {
				{Type: "oauth", Label: "ChatGPT Pro/Plus (browser)"},
				{Type: "oauth", Label: "ChatGPT Pro/Plus (headless)"},
				{Type: "api", Label: "Manually enter API Key"},
			},
			"github-copilot": {{Type: "oauth", Label: "Login with GitHub"}},
			"anthropic":      {{Type: "oauth", Label: "Claude Pro/Max"}},
		},
	}
}

func TestOpencodeLoginChoicesFromCatalog(t *testing.T) {
	ins := withCatalog(t, sampleCatalog(), nil)
	got := LoginChoices(ins)
	var labels []string
	for _, c := range got {
		labels = append(labels, c.Label)
	}
	want := []string{
		"OpenAI — ChatGPT Pro/Plus (headless)",
		"GitHub Copilot — Login with GitHub",
		"OpenAI — ChatGPT Pro/Plus (browser)",
		"Other provider (pick in the terminal)",
	}
	if !slices.Equal(labels, want) {
		t.Fatalf("labels = %q", labels)
	}
	if !got[0].Default || got[0].ID != "openai-headless" {
		t.Fatalf("default = %+v", got[0])
	}
	cases := map[string][]string{
		"openai-headless":  {"auth", "login", "-p", "openai", "-m", "ChatGPT Pro/Plus (headless)"},
		"github-copilot#0": {"auth", "login", "-p", "github-copilot", "-m", "Login with GitHub"},
		"openai#0":         {"auth", "login", "-p", "openai", "-m", "ChatGPT Pro/Plus (browser)"},
		"openai#9":         {"auth", "login", "-p", "openai"},
		"anthropic#0":      {"auth", "login", "-p", "openai", "-m", "ChatGPT Pro/Plus (headless)"},
		"pick":             {"auth", "login"},
	}
	for in, want := range cases {
		if got := opencodeLoginCommand(ins, in); !slices.Equal(got, want) {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestOpencodeAPIKeysFromCatalog(t *testing.T) {
	ins := withCatalog(t, sampleCatalog(), nil)
	ins.Env = []string{"ZHIPU_API_KEY=k"}
	got := APIKeyProviders(ins)
	want := []APIKeyProvider{
		{ID: "anthropic", Label: "Anthropic", Env: "ANTHROPIC_API_KEY"},
		{ID: "openai", Label: "OpenAI", Env: "OPENAI_API_KEY"},
		{ID: "zai", Label: "Z.AI", Env: "ZHIPU_API_KEY", Set: true},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("api keys = %+v", got)
	}
	if v, ok := APIKeyEnvVar(ins, "zai"); !ok || v != "ZHIPU_API_KEY" {
		t.Fatalf("zai env = %q %v", v, ok)
	}
}

// A failed fetch (server unreachable) keeps the static lists.
func TestOpencodeCatalogFallback(t *testing.T) {
	ins := withCatalog(t, nil, context.DeadlineExceeded)
	got := LoginChoices(ins)
	if len(got) != len(OpencodeLoginProviders) || got[0].ID != "openai-headless" || !got[0].Default {
		t.Fatalf("login fallback = %+v", got)
	}
	if len(APIKeyProviders(ins)) != len(opencodeAPIKeyProviders) {
		t.Fatal("api-key fallback")
	}
	if v, ok := APIKeyEnvVar(ins, "google"); !ok || v != "GOOGLE_GENERATIVE_AI_API_KEY" {
		t.Fatalf("google env = %q %v", v, ok)
	}
}
