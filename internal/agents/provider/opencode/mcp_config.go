package opencode

import (
	"encoding/json"
	"os"
	"strings"
)

// mcp_config.go builds the per-spawn OPENCODE_CONFIG_CONTENT.
//
// opencode merges that env as a config layer above the user/project files
// (packages/opencode/src/config/config.ts; docs config.mdx precedence #6)
// and substitutes {env:VAR} inside it (config.mdx "Variables"). The token
// therefore stays in env: the JSON only names the variable.

const (
	mcpServerName  = "wick"
	mcpURLEnvVar   = "WICK_MCP_URL"
	mcpTokenEnvVar = "WICK_MCP_TOKEN"
	configEnvVar   = "OPENCODE_CONFIG_CONTENT"
)

func mcpEndpointFromEnv() string {
	port := strings.TrimSpace(os.Getenv("WICK_PORT"))
	if port == "" {
		return ""
	}
	return "http://127.0.0.1:" + port + "/mcp"
}

// configContent is the inline config for one spawn:
//   - share "disabled": never publish a wick session (docs share.mdx).
//   - permission "allow": headless — nobody can answer an approval prompt
//     (docs permissions.mdx, `"permission": "allow"`).
//   - mcp.wick: remote server (docs mcp-servers.mdx, type "remote" + headers),
//     only when there is an endpoint and a token.
//   - instructions: the wick system prompt file, additive to AGENTS.md
//     (docs rules.mdx "instructions").
func configContent(withMCP bool, soulPath string, extras map[string]map[string]any, disable []string) string {
	// share "disabled" makes opencode refuse every share request
	// (src/share/session.ts:28), which also defeats a user/project config
	// with share "auto" or OPENCODE_AUTO_SHARE — a wick session, with its
	// system prompt and tool output, must never be published to opncd.ai.
	cfg := map[string]any{"permission": "allow", "share": "disabled"}
	mcp := map[string]any{}
	// Servers declared by project/home config files: switched off in this
	// last-merged layer, so only wick + the instance's extras connect.
	for _, name := range disable {
		mcp[name] = map[string]any{"enabled": false}
	}
	for name, e := range extras {
		mcp[name] = e
	}
	if withMCP {
		mcp[mcpServerName] = map[string]any{
			"type":    "remote",
			"url":     "{env:" + mcpURLEnvVar + "}",
			"enabled": true,
			"headers": map[string]any{
				"Authorization": "Bearer {env:" + mcpTokenEnvVar + "}",
			},
		}
	} else if _, clash := mcp[mcpServerName]; clash {
		mcp[mcpServerName] = map[string]any{"enabled": false}
	}
	if len(mcp) > 0 {
		cfg["mcp"] = mcp
	}
	if soulPath != "" {
		cfg["instructions"] = []string{soulPath}
	}
	b, _ := json.Marshal(cfg)
	return string(b)
}

func mcpEnv(endpoint, token string) []string {
	if endpoint == "" || token == "" {
		return nil
	}
	return []string{mcpURLEnvVar + "=" + endpoint, mcpTokenEnvVar + "=" + token}
}
