package claude

import (
	"encoding/json"
	"strings"
	"testing"
)

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

	// --mcp-config injects wick's server and keeps the user's own, plus
	// --allowedTools pre-approves wick's tools for the headless agent.
	def := mcpConfigArgs(ep, tok, "")
	if _, ok := argValue(def, "--mcp-config"); !ok {
		t.Fatalf("args missing --mcp-config: %v", def)
	}
	// Isolation is no longer selectable. wick injects its own server per spawn
	// with that caller's token, so --strict-mcp-config would only drop the
	// user's OTHER servers — never wick's own tools — and the global env switch
	// that used to toggle it could not express a per-user decision.
	for _, a := range def {
		if a == "--strict-mcp-config" {
			t.Fatal("argv isolates MCP; wick merges with the user's own servers")
		}
	}
	allowed, ok := argValue(def, "--allowedTools")
	if !ok {
		t.Fatalf("args missing --allowedTools: %v", def)
	}
	// Server-level allow covers every wick tool (meta + wick_manager_*)
	// dynamically, so new tools never need a static allowlist edit.
	if allowed != "mcp__wick" {
		t.Errorf("--allowedTools = %q, want server-level %q", allowed, "mcp__wick")
	}

	if mcpConfigArgs("", tok, "") != nil || mcpConfigArgs(ep, "", "") != nil {
		t.Fatal("empty endpoint or token must yield nil args")
	}
}

// TestMCPConfigArg_SessionHeader verifies the session id is emitted as the
// X-Wick-Session-Id header when supplied, and omitted when empty.
func TestMCPConfigArg_SessionHeader(t *testing.T) {
	withSession := mcpConfigArg("http://127.0.0.1:9425/mcp", "secret123", "slack-1700000000.000100")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(withSession), &parsed); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	servers, _ := parsed["mcpServers"].(map[string]any)
	wick, _ := servers["wick"].(map[string]any)
	headers, _ := wick["headers"].(map[string]any)
	if headers["X-Wick-Session-Id"] != "slack-1700000000.000100" {
		t.Errorf("session header = %v, want the session id", headers["X-Wick-Session-Id"])
	}

	noSession := mcpConfigArg("http://127.0.0.1:9425/mcp", "secret123", "")
	if strings.Contains(noSession, "X-Wick-Session-Id") {
		t.Errorf("empty session must omit the header, got: %s", noSession)
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
	got := mcpConfigArg("http://127.0.0.1:9425/mcp", "secret123", "")
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
