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

func argValue(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func TestMCPConfigArgs(t *testing.T) {
	ep, tok := "http://127.0.0.1:9425/mcp", "secret123"

	// Default (non-strict): --mcp-config (so the user's own MCP servers
	// keep working) PLUS --allowedTools pre-approving wick's own MCP
	// tools — the agent runs headless, so wick_* calls must not block on
	// a permission prompt nobody can answer.
	def := mcpConfigArgs(ep, tok, false)
	if _, ok := argValue(def, "--mcp-config"); !ok {
		t.Fatalf("default args missing --mcp-config: %v", def)
	}
	for _, a := range def {
		if a == "--strict-mcp-config" {
			t.Fatal("default path must NOT include --strict-mcp-config")
		}
	}
	allowed, ok := argValue(def, "--allowedTools")
	if !ok {
		t.Fatalf("default args missing --allowedTools: %v", def)
	}
	for _, want := range []string{
		"mcp__wick__wick_list",
		"mcp__wick__wick_search",
		"mcp__wick__wick_get",
		"mcp__wick__wick_execute",
		"mcp__wick__wick_list_providers",
	} {
		if !strings.Contains(allowed, want) {
			t.Errorf("--allowedTools %q missing %q", allowed, want)
		}
	}

	// Opt-in strict isolation still carries both --strict-mcp-config and
	// the pre-approved wick tools.
	strict := mcpConfigArgs(ep, tok, true)
	if strict[0] != "--strict-mcp-config" {
		t.Fatalf("strict args = %v, want leading --strict-mcp-config", strict)
	}
	if _, ok := argValue(strict, "--allowedTools"); !ok {
		t.Fatalf("strict args missing --allowedTools: %v", strict)
	}

	if mcpConfigArgs("", tok, false) != nil || mcpConfigArgs(ep, "", false) != nil {
		t.Fatal("empty endpoint or token must yield nil args")
	}
}

func TestMaxTurnsArgs(t *testing.T) {
	if maxTurnsArgs(0) != nil || maxTurnsArgs(-1) != nil {
		t.Fatal("0/negative must yield nil (unlimited)")
	}
	got := maxTurnsArgs(4)
	if len(got) != 2 || got[0] != "--max-turns" || got[1] != "4" {
		t.Fatalf("got %v, want [--max-turns 4]", got)
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
