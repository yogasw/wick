package provider

import (
	"strings"
	"testing"
)

func TestRedactArgv_MasksBearerAndKeepsInput(t *testing.T) {
	cfg := `{"mcpServers":{"wick":{"headers":{"Authorization":"Bearer wick_sub_abc.def-ghi","X-Wick-Session-Id":"s1"}}}}`
	in := []string{"-p", "--mcp-config", cfg, "--allowedTools", "mcp__wick"}
	out := RedactArgv(in)

	if in[2] != cfg {
		t.Fatal("input argv was modified")
	}
	joined := strings.Join(out, " ")
	if strings.Contains(joined, "abc.def-ghi") {
		t.Fatalf("token survived redaction: %s", joined)
	}
	if !strings.Contains(joined, "Bearer [REDACTED]") || !strings.Contains(joined, `"X-Wick-Session-Id":"s1"`) {
		t.Fatalf("unexpected redaction result: %s", joined)
	}
	if out[0] != "-p" || out[4] != "mcp__wick" {
		t.Fatalf("plain args changed: %v", out)
	}
}

func TestRedactSecret_BareWickToken(t *testing.T) {
	got := RedactSecret("token=wick_cli_XYZ123 next")
	if got != "token=wick_cli_[REDACTED] next" {
		t.Fatalf("got %q", got)
	}
}
