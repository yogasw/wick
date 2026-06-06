package claude

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/safeexec"
)

func helpHasStrictMCP(help string) bool {
	return strings.Contains(help, "--mcp-config") && strings.Contains(help, "--strict-mcp-config")
}

func helpHasMCPConfig(help string) bool {
	return strings.Contains(help, "--mcp-config")
}

// mcpConfigArgs builds the claude argv for pointing at the wick MCP HTTP
// server. By default it does NOT add --strict-mcp-config, so the wick
// server MERGES with the user's existing MCP servers (~/.claude.json,
// .mcp.json) rather than replacing them. strict=true opts into isolation
// (only the wick server is visible). Empty endpoint/token → no args.
func mcpConfigArgs(endpoint, token string, strict bool) []string {
	if endpoint == "" || token == "" {
		return nil
	}
	cfg := mcpConfigArg(endpoint, token)
	if strict {
		return []string{"--strict-mcp-config", "--mcp-config", cfg}
	}
	return []string{"--mcp-config", cfg}
}

// mcpEndpointFromEnv derives the loopback MCP URL from WICK_PORT (set
// by the server before any spawn). Empty when unset = stdio fallback.
func mcpEndpointFromEnv() string {
	port := strings.TrimSpace(os.Getenv("WICK_PORT"))
	if port == "" {
		return ""
	}
	return "http://127.0.0.1:" + port + "/mcp"
}

func mcpConfigArg(endpoint, token string) string {
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"wick": map[string]any{
				"type": "http",
				"url":  endpoint,
				"headers": map[string]string{
					"Authorization": "Bearer " + token,
				},
			},
		},
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	return string(b)
}

var mcpHelpCache sync.Map

func strictMCPConfigSupported(bin string) bool {
	if v, ok := mcpHelpCache.Load(bin); ok {
		return v.(bool)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _ := safeexec.CommandContext(ctx, bin, "--help").CombinedOutput()
	ok := helpHasStrictMCP(string(out))
	mcpHelpCache.Store(bin, ok)
	return ok
}

var mcpConfigHelpCache sync.Map

// mcpConfigSupported reports whether the claude binary understands
// --mcp-config (the only flag the default, non-strict path needs).
func mcpConfigSupported(bin string) bool {
	if v, ok := mcpConfigHelpCache.Load(bin); ok {
		return v.(bool)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _ := safeexec.CommandContext(ctx, bin, "--help").CombinedOutput()
	ok := helpHasMCPConfig(string(out))
	mcpConfigHelpCache.Store(bin, ok)
	return ok
}
