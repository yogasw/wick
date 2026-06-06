package claude

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHelpHasStrictMCP(t *testing.T) {
	both := "  --mcp-config <configs...>  Load MCP servers\n  --strict-mcp-config  Only use --mcp-config"
	if !helpHasStrictMCP(both) {
		t.Fatal("expected true when both flags present")
	}
	missingStrict := "  --mcp-config <configs...>  Load MCP servers"
	if helpHasStrictMCP(missingStrict) {
		t.Fatal("expected false when --strict-mcp-config absent")
	}
	if helpHasStrictMCP("") {
		t.Fatal("expected false for empty help")
	}
}

func TestMCPEndpointFromEnv(t *testing.T) {
	t.Setenv("WICK_PORT", "9425")
	if got := mcpEndpointFromEnv(); got != "http://127.0.0.1:9425/mcp" {
		t.Fatalf("got %q, want http://127.0.0.1:9425/mcp", got)
	}
	t.Setenv("WICK_PORT", "")
	if got := mcpEndpointFromEnv(); got != "" {
		t.Fatalf("expected empty when WICK_PORT unset, got %q", got)
	}
}

func TestMCPConfigArg(t *testing.T) {
	got := mcpConfigArg("http://127.0.0.1:9425/mcp", "secret123")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, got)
	}
	servers, _ := parsed["mcpServers"].(map[string]any)
	wick, _ := servers["wick"].(map[string]any)
	if wick["type"] != "http" {
		t.Fatalf("type = %v, want http", wick["type"])
	}
	if wick["url"] != "http://127.0.0.1:9425/mcp" {
		t.Fatalf("url = %v", wick["url"])
	}
	headers, _ := wick["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer secret123" {
		t.Fatalf("auth header = %v", headers["Authorization"])
	}
	if !strings.Contains(got, "Bearer secret123") {
		t.Fatal("token missing from config")
	}
}
