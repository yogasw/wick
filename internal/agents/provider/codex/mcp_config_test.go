package codex

import (
	"strings"
	"testing"
)

// argValue returns the value following flag in args.
func argValue(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// TestMCPConfigArgs_RegistersWickServer checks the -c overrides codex needs.
//
// codex has no --mcp-config equivalent: it takes TOML config overrides and
// reads the bearer from an env var named in that config. Verified against
// codex-cli 0.149.0, where two spawns with different env values reached the
// server with different Authorization headers.
func TestMCPConfigArgs_RegistersWickServer(t *testing.T) {
	args := mcpConfigArgs("http://127.0.0.1:9425/mcp", "wick_sub_tokenA")
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "mcp_servers.wick.url=") {
		t.Fatalf("args do not register the server url: %v", args)
	}
	if !strings.Contains(joined, "mcp_servers.wick.bearer_token_env_var=") {
		t.Fatalf("args do not name the bearer env var: %v", args)
	}
	// The token must NOT be in argv — argv is world-readable in process
	// listings, the environment is not.
	if strings.Contains(joined, "wick_sub_tokenA") {
		t.Fatal("credential leaked into argv; it belongs in the environment")
	}
	// Values are TOML-quoted: codex parses the right side of -c as TOML and
	// only falls back to a raw string, so an unquoted URL is at the mercy of
	// that fallback.
	if v, _ := argValue(args, "-c"); !strings.Contains(v, `"`) {
		t.Errorf("config value is not quoted: %q", v)
	}
}

// TestMCPConfigArgs_NilWithoutEndpointOrToken: a half-configured server would
// make codex fail every tool call at runtime, which is worse than having no
// wick tools at all.
func TestMCPConfigArgs_NilWithoutEndpointOrToken(t *testing.T) {
	if got := mcpConfigArgs("", "tok"); got != nil {
		t.Errorf("no endpoint yielded %v, want nil", got)
	}
	if got := mcpConfigArgs("http://127.0.0.1:9/mcp", ""); got != nil {
		t.Errorf("no token yielded %v, want nil", got)
	}
}

// TestMCPEnv_CarriesTheToken pins where the credential actually travels, and
// that the variable name matches what the argv tells codex to read.
func TestMCPEnv_CarriesTheToken(t *testing.T) {
	env := mcpEnv("wick_sub_tokenA")
	if len(env) != 1 || env[0] != mcpTokenEnvVar+"=wick_sub_tokenA" {
		t.Fatalf("env = %v, want %s=<token>", env, mcpTokenEnvVar)
	}
	// The argv must point at this exact variable, or codex reads nothing.
	args := strings.Join(mcpConfigArgs("http://127.0.0.1:9/mcp", "tok"), " ")
	if !strings.Contains(args, mcpTokenEnvVar) {
		t.Fatalf("argv does not reference %s: %s", mcpTokenEnvVar, args)
	}
	if got := mcpEnv(""); got != nil {
		t.Errorf("empty token yielded %v, want nil", got)
	}
}

// TestMCPEnv_DistinctPerUser is the point of the whole change: two callers must
// produce two different credentials, or one user's spawn acts as another.
func TestMCPEnv_DistinctPerUser(t *testing.T) {
	a := mcpEnv("wick_sub_tokenA")
	b := mcpEnv("wick_sub_tokenB")
	if len(a) != 1 || len(b) != 1 || a[0] == b[0] {
		t.Fatalf("two users share one credential: %v vs %v", a, b)
	}
}

// TestQuoteTOML_EscapesQuotes keeps a hostile value from breaking out of the
// TOML string and injecting another config key.
func TestQuoteTOML_EscapesQuotes(t *testing.T) {
	got := quoteTOML(`ab"cd`)
	if got != `"ab\"cd"` {
		t.Fatalf("quoteTOML = %s, want an escaped quote", got)
	}
}
