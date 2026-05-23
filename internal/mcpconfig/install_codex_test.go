package mcpconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUninstallCodexTOML_DottedTable(t *testing.T) {
	input := `[settings]
model = "o4-mini"

[mcp_servers.wick-lab]
type = "stdio"
command = "D:\\code\\work\\wick\\bin\\wick-lab.exe"
args = ["mcp", "serve"]

[other]
x = 1
`
	f := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(f, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := uninstallCodexTOML(f, "wick-lab"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	got, _ := os.ReadFile(f)
	result := string(got)

	if strings.Contains(result, "wick-lab") {
		t.Errorf("wick-lab still present:\n%s", result)
	}
	if !strings.Contains(result, "[other]") {
		t.Errorf("[other] section missing:\n%s", result)
	}
}

func TestUninstallCodexTOML_ActualConfig(t *testing.T) {
	// Mirrors the actual ~/.codex/config.toml structure including
	// [projects.'path'] headers with special chars.
	input := "[windows]\nsandbox = \"elevated\"\n\n[projects.'C:\\Users\\Staffinc']\ntrust_level = \"trusted\"\n\n[projects.'d:\\code\\work\\wick']\ntrust_level = \"trusted\"\n\n[tui.model_availability_nux]\n\"gpt-5.5\" = 4\n\n[mcp_servers.wick-lab]\ntype = \"stdio\"\ncommand = \"D:\\\\code\\\\work\\\\wick\\\\bin\\\\wick-lab.exe\"\nargs = [\"mcp\", \"serve\"]\n"

	f := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(f, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := uninstallCodexTOML(f, "wick-lab"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	got, _ := os.ReadFile(f)
	result := string(got)

	if strings.Contains(result, "wick-lab") {
		t.Errorf("wick-lab still present:\n%s", result)
	}
	if !strings.Contains(result, "[windows]") {
		t.Errorf("[windows] section missing:\n%s", result)
	}
	if !strings.Contains(result, "trust_level") {
		t.Errorf("[projects] entries missing:\n%s", result)
	}
}

func TestUninstallCodexTOML_ArrayOfTables(t *testing.T) {
	input := `[settings]
model = "o4-mini"

[[mcp_servers]]
name = "wick-lab"
type = "stdio"
cmd = "D:\\code\\work\\wick\\bin\\wick-lab.exe"
args = ["mcp", "serve"]

[[mcp_servers]]
name = "other"
type = "stdio"
cmd = "other.exe"
args = []
`
	f := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(f, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := uninstallCodexTOML(f, "wick-lab"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}

	got, _ := os.ReadFile(f)
	result := string(got)

	if strings.Contains(result, "wick-lab") {
		t.Errorf("wick-lab still present:\n%s", result)
	}
	if !strings.Contains(result, "other") {
		t.Errorf("other server removed unexpectedly:\n%s", result)
	}
}

func TestInstallCodexTOML_WritesEnvVars(t *testing.T) {
	f := filepath.Join(t.TempDir(), "config.toml")
	entry := map[string]any{
		"command":  "/usr/local/bin/app",
		"args":     []string{"mcp", "serve"},
		"env_vars": []string{"DATABASE_URL", "WICK_ENC_KEY"},
	}
	if err := installCodexTOML(f, "support-tools", entry); err != nil {
		t.Fatalf("install: %v", err)
	}
	got, _ := os.ReadFile(f)
	result := string(got)
	if !strings.Contains(result, `env_vars = ["DATABASE_URL", "WICK_ENC_KEY"]`) {
		t.Errorf("env_vars line missing or malformed:\n%s", result)
	}
}

func TestInstallCodexTOML_OmitsEnvVarsWhenEmpty(t *testing.T) {
	f := filepath.Join(t.TempDir(), "config.toml")
	entry := map[string]any{
		"command": "/usr/local/bin/app",
		"args":    []string{"mcp", "serve"},
	}
	if err := installCodexTOML(f, "support-tools", entry); err != nil {
		t.Fatalf("install: %v", err)
	}
	got, _ := os.ReadFile(f)
	if strings.Contains(string(got), "env_vars") {
		t.Errorf("env_vars line present when entry omits it:\n%s", got)
	}
}

func TestInstallCodexTOML_NoDuplicate(t *testing.T) {
	input := `[mcp_servers.wick-lab]
type = "stdio"
command = "D:\\code\\work\\wick\\bin\\wick-lab.exe"
args = ["mcp", "serve"]
`
	f := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(f, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	entry := map[string]any{"command": `D:\code\work\wick\bin\wick-lab.exe`, "args": []string{"mcp", "serve"}}
	if err := installCodexTOML(f, "wick-lab", entry); err != nil {
		t.Fatalf("install: %v", err)
	}

	got, _ := os.ReadFile(f)
	result := string(got)

	count := 0
	for _, line := range strings.Split(result, "\n") {
		if line == "[mcp_servers.wick-lab]" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 entry, got %d:\n%s", count, result)
	}
}
