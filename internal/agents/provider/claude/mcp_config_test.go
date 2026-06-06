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

func TestHelpHasMCPConfig(t *testing.T) {
	if !helpHasMCPConfig("  --mcp-config <configs...>  Load MCP servers") {
		t.Fatal("expected true when --mcp-config present")
	}
	if helpHasMCPConfig("  --foo  bar") {
		t.Fatal("expected false when --mcp-config absent")
	}
	if helpHasMCPConfig("") {
		t.Fatal("expected false for empty help")
	}
}

func TestMCPConfigArgs(t *testing.T) {
	ep, tok := "http://127.0.0.1:9425/mcp", "secret123"

	// Default (non-strict): --mcp-config only, NO --strict-mcp-config, so
	// the user's own MCP servers keep working (the regression fix).
	def := mcpConfigArgs(ep, tok, false)
	if len(def) != 2 || def[0] != "--mcp-config" {
		t.Fatalf("default args = %v, want [--mcp-config <json>]", def)
	}
	for _, a := range def {
		if a == "--strict-mcp-config" {
			t.Fatal("default path must NOT include --strict-mcp-config")
		}
	}

	// Opt-in strict isolation.
	strict := mcpConfigArgs(ep, tok, true)
	if len(strict) != 3 || strict[0] != "--strict-mcp-config" || strict[1] != "--mcp-config" {
		t.Fatalf("strict args = %v, want [--strict-mcp-config --mcp-config <json>]", strict)
	}

	if mcpConfigArgs("", tok, false) != nil || mcpConfigArgs(ep, "", false) != nil {
		t.Fatal("empty endpoint or token must yield nil args")
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
