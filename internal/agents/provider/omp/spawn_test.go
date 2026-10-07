package omp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

func TestBuildArgsProfileFirstAndResume(t *testing.T) {
	ins := provider.Instance{Type: provider.TypeOMP, Name: "work", OMPConfig: &provider.OMPConfig{Profile: "wick-old-name"}}
	opt := provider.SpawnOptions{Workspace: "/w", ResumeID: "sid-1", ModelID: "openai-codex/gpt-5.2", Instance: &ins}
	got := buildArgs(ins, opt, "/s/.omp/soul.md", "/s/.omp/wick-settings.yml", nil)
	want := []string{"--profile", "wick-old-name", "-p", "--mode", "json", "--no-title", "--auto-approve",
		"--config", "/s/.omp/wick-settings.yml",
		"--cwd", "/w", "--append-system-prompt", "/s/.omp/soul.md",
		"--model", "openai-codex/gpt-5.2", "--resume", "sid-1"}
	if !slices.Equal(got, want) {
		t.Fatalf("argv\n got %q\nwant %q", got, want)
	}
}

func TestBuildArgsFreshSessionNoResume(t *testing.T) {
	ins := provider.Instance{Type: provider.TypeOMP, Name: "My Omp"}
	got := buildArgs(ins, provider.SpawnOptions{}, "", "", nil)
	if got[1] != "wick-my-omp" || slices.Contains(got, "--resume") || slices.Contains(got, "--cwd") {
		t.Fatalf("argv = %q", got)
	}
}

func TestEnsureMCPConfigMergesAndIsIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "mcp.json")
	os.WriteFile(path, []byte(`{"mcpServers":{"other":{"type":"stdio","command":"x"}}}`), 0o600)
	if err := ensureMCPConfig(dir, true, nil); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.MCPServers["other"] == nil {
		t.Fatal("operator's server dropped")
	}
	w := doc.MCPServers["wick"]
	if w["url"] != "${WICK_MCP_URL}" || !strings.Contains(string(b), "Bearer ${WICK_MCP_TOKEN}") {
		t.Fatalf("wick entry = %v", w)
	}
	if strings.Contains(string(b), "tok-secret") {
		t.Fatal("token written to disk")
	}
	st1, _ := os.Stat(path)
	if err := ensureMCPConfig(dir, true, nil); err != nil {
		t.Fatal(err)
	}
	st2, _ := os.Stat(path)
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Error("rewrote an already-current file")
	}
}

func TestEnsureMCPConfigRefusesInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(`{nope`), 0o600)
	if err := ensureMCPConfig(dir, true, nil); err == nil {
		t.Fatal("expected error, file must not be clobbered")
	}
}

func TestProfileAgentDir(t *testing.T) {
	if got := profileAgentDir("/h", "", "wick-a"); got != filepath.Join("/h", ".omp", "profiles", "wick-a", "agent") {
		t.Fatal(got)
	}
	if got := profileAgentDir("/h", ".pi", "p"); got != filepath.Join("/h", ".pi", "profiles", "p", "agent") {
		t.Fatal(got)
	}
}

func TestMCPEnvNeedsBoth(t *testing.T) {
	if mcpEnv("", "t") != nil || mcpEnv("u", "") != nil {
		t.Fatal("partial MCP env must be nil")
	}
	if got := mcpEnv("http://x/mcp", "t"); len(got) != 2 {
		t.Fatal(got)
	}
}

func TestEnsureMCPConfigExtrasAndCleanup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.json")
	os.WriteFile(path, []byte(`{"mcpServers":{"mine":{"type":"stdio","command":"x"}}}`), 0o600)
	extras := map[string]map[string]any{"gh": {"type": "http", "url": "https://api.gh/mcp", "headers": map[string]string{"Authorization": "Bearer ${GH_TOKEN}"}}}
	if err := ensureMCPConfig(dir, true, extras); err != nil {
		t.Fatal(err)
	}
	var doc struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	read := func() {
		b, _ := os.ReadFile(path)
		doc.MCPServers = nil
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatal(err)
		}
	}
	read()
	if doc.MCPServers["mine"] == nil || doc.MCPServers["gh"] == nil || doc.MCPServers["wick"] == nil {
		t.Fatalf("after merge: %v", doc.MCPServers)
	}
	// extra removed from the instance → removed from the file; operator's stays
	if err := ensureMCPConfig(dir, true, nil); err != nil {
		t.Fatal(err)
	}
	read()
	if doc.MCPServers["gh"] != nil || doc.MCPServers["mine"] == nil || doc.MCPServers["wick"] == nil {
		t.Fatalf("after cleanup: %v", doc.MCPServers)
	}
	// no MCP token for this spawn → wick's own entry is withdrawn too
	if err := ensureMCPConfig(dir, false, nil); err != nil {
		t.Fatal(err)
	}
	read()
	if doc.MCPServers["wick"] != nil || doc.MCPServers["mine"] == nil {
		t.Fatalf("after no-token spawn: %v", doc.MCPServers)
	}
}

func TestIsolationOverlay(t *testing.T) {
	var cfg map[string]any
	if err := json.Unmarshal(isolationOverlay(), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["mcp"].(map[string]any)["enableProjectConfig"] != false {
		t.Fatal("project MCP not disabled")
	}
	if ep, ok := cfg["enabledProviders"].([]any); !ok || len(ep) != 0 {
		t.Fatal("foreign user providers not pinned off")
	}
	if _, bad := cfg["disabledProviders"]; bad {
		t.Fatal("disabledProviders would switch off foreign skills too")
	}
}

func TestExtraMCPEntriesRejectsPlaintext(t *testing.T) {
	ins := provider.Instance{Type: provider.TypeOMP, ExtraMCPServers: `{"gh":{"url":"https://a","headers":{"Authorization":"Bearer ghp_x"}}}`}
	if _, err := extraMCPEntries(ins); err == nil {
		t.Fatal("plaintext secret accepted")
	}
}
