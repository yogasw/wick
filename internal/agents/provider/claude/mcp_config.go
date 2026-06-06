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
