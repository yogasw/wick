package codex

import (
	"os"
	"strings"
)

// mcp_config.go points a codex spawn at wick's own MCP server, carrying the
// per-user credential minted for whoever triggered the spawn.
//
// Shape differs from claude on purpose. claude accepts a whole JSON config on
// the command line (--mcp-config '{...headers...}'), so the token rides in the
// argv. codex has no such flag; it takes config overrides (-c key=value) and
// reads the bearer from an ENVIRONMENT VARIABLE named in that config. So the
// token goes in the env and the argv only names which variable to read.
//
// Keeping the secret out of argv is a side benefit: argv is world-readable in
// process listings, env is not.

// mcpServerName is the key codex files this server under, and the name its
// tools are prefixed with. Matches claude's "wick" so tool names agree across
// providers.
const mcpServerName = "wick"

// mcpTokenEnvVar is the variable codex reads the bearer from. Per-spawn, so two
// users' processes never see each other's token.
const mcpTokenEnvVar = "WICK_MCP_TOKEN"

// mcpEndpointFromEnv derives the loopback MCP URL from WICK_PORT, set by the
// server before any spawn. Empty when unset — the caller then skips MCP
// entirely rather than pointing codex at a dead address.
func mcpEndpointFromEnv() string {
	port := strings.TrimSpace(os.Getenv("WICK_PORT"))
	if port == "" {
		return ""
	}
	return "http://127.0.0.1:" + port + "/mcp"
}

// mcpConfigArgs builds the -c overrides that register wick's MCP server.
//
// Returns nil when either input is missing: a server entry with no endpoint or
// no credential would make codex fail every tool call at runtime, which is
// worse than having no wick tools at all.
func mcpConfigArgs(endpoint, token string) []string {
	if endpoint == "" || token == "" {
		return nil
	}
	return []string{
		"-c", "mcp_servers." + mcpServerName + ".url=" + quoteTOML(endpoint),
		"-c", "mcp_servers." + mcpServerName + ".bearer_token_env_var=" + quoteTOML(mcpTokenEnvVar),
	}
}

// mcpEnv returns the environment entry carrying the token itself.
func mcpEnv(token string) []string {
	if token == "" {
		return nil
	}
	return []string{mcpTokenEnvVar + "=" + token}
}

// quoteTOML wraps a value in double quotes. codex parses the right-hand side of
// -c as TOML and only falls back to a raw string when that fails, so an
// unquoted URL (with its colons and slashes) is at the mercy of that fallback.
// Quoting makes it unambiguously a string.
func quoteTOML(v string) string {
	return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
}
