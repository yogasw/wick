package provider

import (
	"strings"
	"testing"
)

func TestUnmaskSpawnEnv(t *testing.T) {
	isolateConfig(t)
	SetSecretDecrypter(nil)
	t.Cleanup(func() { SetSecretDecrypter(nil) })

	// A claude instance with a custom secret env var + a 9router key.
	if err := Save(Instance{
		Type:          TypeClaude,
		Name:          "work",
		Env:           []string{"MY_API_KEY=supersecret", "PLAIN_VAR=keep"},
		Router9APIKey: "", // empty → Router9AuthKey returns the default
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	stored := []string{
		"CLAUDE_CONFIG_DIR=C:/x/.claude",             // non-secret, verbatim
		"ANTHROPIC_BASE_URL=http://127.0.0.1:9425/v1", // non-secret, verbatim
		"ANTHROPIC_AUTH_TOKEN=s********r",             // router9 key → default
		"MY_API_KEY=s********t",                       // from ins.Env
		"MAX_THINKING_TOKENS=8000",                    // non-secret, per-spawn, verbatim
	}

	got := UnmaskSpawnEnv(TypeClaude, "work", stored)

	if len(got) != len(stored) {
		t.Fatalf("length changed: got %d want %d", len(got), len(stored))
	}
	// Order + non-secret entries preserved verbatim.
	if got[0] != stored[0] || got[1] != stored[1] || got[4] != stored[4] {
		t.Fatalf("non-secret entries mutated: %+v", got)
	}
	// Router9 auth key resolved to the default.
	if got[2] != "ANTHROPIC_AUTH_TOKEN="+router9DefaultKey {
		t.Fatalf("auth token = %q, want default", got[2])
	}
	// Other secret key resolved from instance env.
	if got[3] != "MY_API_KEY=supersecret" {
		t.Fatalf("MY_API_KEY = %q, want unmasked from ins.Env", got[3])
	}
}

func TestUnmaskSpawnEnv_UnknownInstance(t *testing.T) {
	isolateConfig(t)
	stored := []string{"ANTHROPIC_AUTH_TOKEN=s****r"}
	got := UnmaskSpawnEnv(TypeClaude, "ghost", stored)
	if len(got) != 1 || got[0] != stored[0] {
		t.Fatalf("unknown instance should return input unchanged, got %+v", got)
	}
}

func TestUnmaskSpawnEnv_SecretAbsentFromConfig(t *testing.T) {
	isolateConfig(t)
	SetSecretDecrypter(nil)
	t.Cleanup(func() { SetSecretDecrypter(nil) })
	if err := Save(Instance{Type: TypeClaude, Name: "bare"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// A secret key that the live config has no value for stays masked
	// (router9 keys always resolve, so use a different key here).
	stored := []string{"UNKNOWN_TOKEN=a****z"}
	got := UnmaskSpawnEnv(TypeClaude, "bare", stored)
	if got[0] != "UNKNOWN_TOKEN=a****z" {
		t.Fatalf("absent secret should stay masked, got %q", got[0])
	}
	if !strings.Contains(got[0], "****") {
		t.Fatalf("expected still-masked value, got %q", got[0])
	}
}
