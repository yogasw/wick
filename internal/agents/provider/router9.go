package provider

import (
	"os"
	"strings"
)

// Router9 wiring: helpers the spawners use when an instance has
// Use9router enabled. Base URL is derived from WICK_PORT (set by the
// server before any spawn) so the spawned CLI talks to wick's own
// /9router/v1 proxy, which forwards to the local 9router process.
//
// The API key is stored encrypted in the instance config; the provider
// package can't import the configs service, so a decrypter is injected
// at boot via SetSecretDecrypter (same pattern as SetAutoRescanLookup).

// router9DefaultKey is used when the instance has no custom key. 9router
// treats a bare "sk_9router" as its default credential.
const router9DefaultKey = "sk_9router"

// Router9Slot is one named model slot a provider type exposes when routed
// through 9router. Which slots exist is provider-specific (claude has
// Opus/Sonnet/Haiku; codex has a primary Model + a Subagent model). The
// FE renders one model picker per slot; the spawner maps each slot's
// chosen model onto the right CLI flag/config key.
type Router9Slot struct {
	// Key is the stable slot identifier stored in Router9Models (e.g.
	// "opus", "model", "subagent").
	Key string `json:"key"`
	// Label is the human-facing name shown in the form (e.g. "Claude Opus").
	Label string `json:"label"`
	// Placeholder is an example model id for the picker's empty state.
	Placeholder string `json:"placeholder"`
}

// Router9Slots returns the model slots a provider type exposes under
// 9router. Empty for unsupported types (only claude/codex today). Defined
// here in the BE so the count/shape can differ per provider without FE
// changes — the form renders whatever this returns. All slots are optional;
// an unset slot is simply omitted at spawn.
func Router9Slots(t Type) []Router9Slot {
	switch t {
	case TypeClaude:
		// The claude CLI routes three tiers via ANTHROPIC_DEFAULT_*_MODEL.
		return []Router9Slot{
			{Key: "opus", Label: "Claude Opus", Placeholder: "cc/claude-opus-4-6"},
			{Key: "sonnet", Label: "Claude Sonnet", Placeholder: "cc/claude-sonnet-4-6"},
			{Key: "haiku", Label: "Claude Haiku", Placeholder: "cc/claude-haiku-4-5"},
		}
	case TypeCodex:
		return []Router9Slot{
			{Key: "model", Label: "Model", Placeholder: "provider/model-id"},
			{Key: "subagent", Label: "Subagent Model", Placeholder: "defaults to main model"},
		}
	default:
		return nil
	}
}

// secretDecrypter turns a stored wick_cenc_/wick_enc_ token back into
// plaintext. nil until wired; when nil, tokens pass through unchanged
// (safe for dev/tests where encryption is disabled and the stored value
// is already plaintext).
var secretDecrypter func(string) (string, error)

// SetSecretDecrypter wires the boot-time secret decrypter used to unwrap
// the stored 9router API key at spawn. Backed by configs.Service.DecryptSecret.
func SetSecretDecrypter(fn func(string) (string, error)) { secretDecrypter = fn }

// Router9BaseURL returns the wick-origin base URL the CLI should use as
// its 9router endpoint, e.g. "http://127.0.0.1:9425/9router/v1". Empty
// when WICK_PORT is unset (spawn can't reach the proxy — caller should
// treat Use9router as inert).
func Router9BaseURL() string {
	port := strings.TrimSpace(os.Getenv("WICK_PORT"))
	if port == "" {
		return ""
	}
	return "http://127.0.0.1:" + port + "/9router/v1"
}

// Router9AuthKey resolves the plaintext API key for an instance: the
// decrypted custom key when set, else the default. Decryption failure
// falls back to the default rather than leaking the raw token.
func Router9AuthKey(ins Instance) string {
	tok := strings.TrimSpace(ins.Router9APIKey)
	if tok == "" {
		return router9DefaultKey
	}
	if secretDecrypter == nil {
		return tok
	}
	plain, err := secretDecrypter(tok)
	if err != nil || strings.TrimSpace(plain) == "" {
		return router9DefaultKey
	}
	return plain
}

