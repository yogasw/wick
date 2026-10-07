package logintty

// API-key login for omp / opencode.
//
// Neither CLI has a non-interactive "store this key" command that writes
// its own store (omp `login` is OAuth-only; opencode `auth login -p` asks
// for the key on a prompt), but both read the provider's key from a
// well-known env var at spawn. So an API key is saved as an ordinary
// instance Env entry — the same mechanism, masking and spawn path as any
// other secret env — under the variable the CLI actually reads.

import (
	"sort"
	"strings"

	"github.com/yogasw/wick/internal/agents/provider"
)

// APIKeyProvider is one choice in the API-key picker.
type APIKeyProvider struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Env   string `json:"env"`
	// Set reports whether the instance Env already carries this var.
	Set bool `json:"set"`
}

// opencodeAPIKeyProviders are the fallback while no server catalog is at
// hand (see opencodeAPIKeyChoices): models.dev provider ids opencode reads a key
// for, with the env var models.dev declares. Source: the env vars
// opencode's own test preload clears before every run
// (packages/opencode/test/preload.ts, opencode 7945de208) — the ones its
// provider loader would otherwise pick up.
var opencodeAPIKeyProviders = []APIKeyProvider{
	{ID: "anthropic", Label: "Anthropic", Env: "ANTHROPIC_API_KEY"},
	{ID: "openai", Label: "OpenAI", Env: "OPENAI_API_KEY"},
	{ID: "google", Label: "Google (Gemini)", Env: "GOOGLE_GENERATIVE_AI_API_KEY"},
	{ID: "openrouter", Label: "OpenRouter", Env: "OPENROUTER_API_KEY"},
	{ID: "groq", Label: "Groq", Env: "GROQ_API_KEY"},
	{ID: "mistral", Label: "Mistral", Env: "MISTRAL_API_KEY"},
	{ID: "perplexity", Label: "Perplexity", Env: "PERPLEXITY_API_KEY"},
	{ID: "xai", Label: "xAI", Env: "XAI_API_KEY"},
	{ID: "deepseek", Label: "DeepSeek", Env: "DEEPSEEK_API_KEY"},
	{ID: "cerebras", Label: "Cerebras", Env: "CEREBRAS_API_KEY"},
}

// opencodeAPIKeyChoices are the providers opencode's own server lists
// (GET /provider all[]) that read a key from env: {id, name, env[0]}.
// Falls back to opencodeAPIKeyProviders without a catalog.
func opencodeAPIKeyChoices(ins provider.Instance) []APIKeyProvider {
	cat := provider.PeekOpencodeCatalog(ins)
	if cat == nil {
		return opencodeAPIKeyProviders
	}
	var out []APIKeyProvider
	for _, p := range cat.Providers {
		if len(p.Env) == 0 || strings.TrimSpace(p.Env[0]) == "" {
			continue
		}
		label := p.Name
		if label == "" {
			label = p.ID
		}
		out = append(out, APIKeyProvider{ID: p.ID, Label: label, Env: strings.TrimSpace(p.Env[0])})
	}
	if len(out) == 0 {
		return opencodeAPIKeyProviders
	}
	return out
}

// APIKeyProviders lists ins's API-key choices, each marked Set when the
// instance Env already holds a non-empty value for its var. Sorted by label.
func APIKeyProviders(ins provider.Instance) []APIKeyProvider {
	var src []APIKeyProvider
	switch ins.Type {
	case provider.TypeOMP:
		src = ompAPIKeyProviders
	case provider.TypeOpencode:
		src = opencodeAPIKeyChoices(ins)
	default:
		return nil
	}
	out := make([]APIKeyProvider, len(src))
	for i, p := range src {
		p.Set = envValue(ins.Env, p.Env) != ""
		out[i] = p
	}
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Label) < strings.ToLower(out[j].Label)
	})
	return out
}

// APIKeyEnvVar resolves a provider id to the env var ins reads its key from.
func APIKeyEnvVar(ins provider.Instance, id string) (string, bool) {
	for _, p := range APIKeyProviders(ins) {
		if p.ID == id {
			return p.Env, true
		}
	}
	return "", false
}

// SetEnvVar returns env with key set to value (replacing any existing
// entry, keeping order), or removed when value is "".
func SetEnvVar(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			if value != "" && !replaced {
				out = append(out, prefix+value)
			}
			replaced = true
			continue
		}
		out = append(out, kv)
	}
	if !replaced && value != "" {
		out = append(out, prefix+value)
	}
	return out
}
