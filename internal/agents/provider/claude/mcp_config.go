package claude

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/internal/safeexec"
)

// maxTurnsArgs builds the --max-turns argv. n <= 0 = no cap (provider
// default / unlimited), so no flag is emitted.
func maxTurnsArgs(n int) []string {
	if n <= 0 {
		return nil
	}
	return []string{"--max-turns", strconv.Itoa(n)}
}

func helpHasStrictMCP(help string) bool {
	return strings.Contains(help, "--mcp-config") && strings.Contains(help, "--strict-mcp-config")
}

func helpHasMCPConfig(help string) bool {
	return strings.Contains(help, "--mcp-config")
}

// wickMCPAllowedTools pre-approves wick's own MCP meta tools so the
// headless agent isn't blocked on a permission prompt nobody can answer.
const wickMCPAllowedTools = "mcp__wick__wick_list,mcp__wick__wick_search,mcp__wick__wick_get,mcp__wick__wick_execute,mcp__wick__wick_list_providers"

// mcpConfigArgs builds the claude argv for the wick MCP HTTP server.
// strict=true isolates to only wick; always pre-approves wick's tools.
func mcpConfigArgs(endpoint, token string, strict bool) []string {
	if endpoint == "" || token == "" {
		return nil
	}
	cfg := mcpConfigArg(endpoint, token)
	args := []string{}
	if strict {
		args = append(args, "--strict-mcp-config")
	}
	args = append(args, "--mcp-config", cfg, "--allowedTools", wickMCPAllowedTools)
	return args
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
