package claude

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yogasw/wick/pkg/safeexec"
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

// wickMCPAllowedTools pre-approves every tool from wick's own MCP server
// so the headless agent isn't blocked on a permission prompt nobody can
// answer. Server-level form ("mcp__<server>") covers the meta-tools AND
// the dynamic wick_manager_* surface without a static per-tool list.
// Not a security boundary — wick enforces per-op access server-side
// (e.g. wickmanager's requireAdmin/requireTray gates).
const wickMCPAllowedTools = "mcp__wick"

// mcpConfigArgs builds the claude argv for the wick MCP HTTP server.
// strict=true isolates to only wick; always pre-approves wick's tools.
// sessionID, when non-empty, is sent as the X-Wick-Session-Id header on
// every MCP call so the server knows which session a connector call belongs
// to WITHOUT relying on the LLM to pass a session_id argument — used e.g. to
// resolve the session-owning Slack bot for the "Sent using @bot" footer.
func mcpConfigArgs(endpoint, token, sessionID string, strict bool) []string {
	if endpoint == "" || token == "" {
		return nil
	}
	cfg := mcpConfigArg(endpoint, token, sessionID)
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

func mcpConfigArg(endpoint, token, sessionID string) string {
	headers := map[string]string{
		"Authorization": "Bearer " + token,
	}
	if sessionID != "" {
		headers["X-Wick-Session-Id"] = sessionID
	}
	cfg := map[string]any{
		"mcpServers": map[string]any{
			"wick": map[string]any{
				"type":    "http",
				"url":     endpoint,
				"headers": headers,
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
