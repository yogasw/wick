package logintty

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"github.com/yogasw/wick/internal/agents/provider"
)

// health_key.go names the account behind an instance for the failover
// health registry (provider/health.go). Like UsageIdentity it may only
// MERGE instances that are provably one login; anything it cannot tell
// apart falls back to "type/name" (one key per instance), never to one
// key shared by every instance of a type.

func init() {
	provider.HealthKeyFunc = HealthKey
}

// geminiIdentityEnv are the env keys that pick a gemini account. The
// gemini CLI keeps its OAuth login in ~/.gemini for every instance, so
// only these tell two instances apart.
var geminiIdentityEnv = []string{
	"GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_APPLICATION_CREDENTIALS",
	"GEMINI_CLI_HOME", "GOOGLE_CLOUD_PROJECT",
}

// HealthKey is the registry key of an instance, "" for "use type/name".
//   - types with a usage API: UsageIdentity, the same key the usage probe
//     and its file use, so the registry reads what the probe wrote;
//   - gemini: a hash of the account-selecting env, when any is set;
//   - everything else (wick, airouter, a bare gemini): "".
func HealthKey(ins provider.Instance) string {
	env := provider.AccountEnv(ins)
	// An instance running on an API key is that key's account, not the
	// OAuth login sitting in its config dir.
	if keys := apiKeyEnv[ins.Type]; len(keys) > 0 {
		if k := envHealthKey(string(ins.Type), env, keys); k != "" {
			return k
		}
	}
	if SupportsUsage(ins.Type) {
		id := UsageIdentity(ins.Type, env)
		if strings.HasSuffix(id, ":dir:") {
			return "" // no config dir to tell accounts apart
		}
		return id
	}
	if ins.Type == provider.TypeGemini {
		return envHealthKey("gemini", env, geminiIdentityEnv)
	}
	return ""
}

// apiKeyEnv are the env keys that put an instance on an API-key account.
var apiKeyEnv = map[provider.Type][]string{
	provider.TypeClaude: {"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_BASE_URL"},
	provider.TypeCodex:  {"OPENAI_API_KEY", "CODEX_API_KEY", "OPENAI_BASE_URL"},
}

// envHealthKey is "<type>:env:<hash>" of the given env keys, "" when
// none is set. Hashed: the key lands in health.json and must not carry a
// plaintext secret.
func envHealthKey(typ string, env []string, keys []string) string {
	var parts []string
	for _, kv := range env {
		for _, k := range keys {
			if v, ok := strings.CutPrefix(kv, k+"="); ok && v != "" {
				parts = append(parts, k+"="+v)
			}
		}
	}
	if len(parts) == 0 {
		return ""
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return typ + ":env:" + hex.EncodeToString(sum[:8])
}
