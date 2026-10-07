package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

func TestBuildArgsResumeAndModel(t *testing.T) {
	got := buildArgs(provider.SpawnOptions{ResumeID: "ses_1"}, nil, "openai/gpt-5.2", false)
	want := []string{"run", "--format", "json", "--thinking", "--auto", "--model", "openai/gpt-5.2", "--session", "ses_1"}
	if !slices.Equal(got, want) {
		t.Fatalf("argv\n got %q\nwant %q", got, want)
	}
	// --model already in the instance args: not added twice
	got = buildArgs(provider.SpawnOptions{ExtraArgs: []string{"-m", "openai/x"}}, nil, "openai/x", true)
	if n := strings.Count(strings.Join(got, " "), "openai/x"); n != 1 || slices.Contains(got, "--session") {
		t.Fatalf("argv %q", got)
	}
}

func cfgOf(t *testing.T, env []string) map[string]any {
	t.Helper()
	for _, kv := range env {
		if strings.HasPrefix(kv, configEnvVar+"=") {
			var cfg map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(kv, configEnvVar+"=")), &cfg); err != nil {
				t.Fatal(err)
			}
			return cfg
		}
	}
	t.Fatal("no inline config")
	return nil
}

func TestSpawnEnvIsolatesAndMerges(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "oc-a")
	ins := provider.Instance{Type: provider.TypeOpencode, Name: "renamed", OpencodeConfig: &provider.OpencodeConfig{DataDir: dir},
		ExtraMCPServers: `{"mcpServers":{"gh":{"url":"https://api.gh/mcp","headers":{"Authorization":"Bearer ${GH_TOKEN}"}}}}`}
	env, err := spawnEnv(ins, "/s/soul.md", "http://127.0.0.1:1/mcp", "tok-secret", []string{"hostmcp", "gh"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"XDG_DATA_HOME=" + dir, "XDG_CONFIG_HOME=" + filepath.Join(dir, "config"),
		"OPENCODE_DISABLE_AUTOUPDATE=true", "OPENCODE_CONFIG=", "OPENCODE_CONFIG_DIR=", "OPENCODE_AUTO_SHARE=false", "WICK_MCP_TOKEN=tok-secret"} {
		if !slices.Contains(env, want) {
			t.Errorf("env missing %q", want)
		}
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, configEnvVar+"=") && strings.Contains(kv, "tok-secret") {
			t.Fatal("token inlined into config")
		}
	}
	cfg := cfgOf(t, env)
	if cfg["permission"] != "allow" || cfg["share"] != "disabled" {
		t.Errorf("cfg %v", cfg)
	}
	mcp := cfg["mcp"].(map[string]any)
	if w := mcp["wick"].(map[string]any); w["type"] != "remote" || w["url"] != "{env:WICK_MCP_URL}" {
		t.Errorf("wick %v", w)
	}
	if h := mcp["hostmcp"].(map[string]any); h["enabled"] != false {
		t.Errorf("host server not disabled: %v", h)
	}
	gh := mcp["gh"].(map[string]any)
	if gh["enabled"] != true || gh["headers"].(map[string]any)["Authorization"] != "Bearer {env:GH_TOKEN}" {
		t.Errorf("extra overridden by the disable list or not converted: %v", gh)
	}
	if ins := cfg["instructions"].([]any); ins[0] != "/s/soul.md" {
		t.Errorf("instructions = %v", ins)
	}
}

func TestSpawnEnvRejectsBadExtras(t *testing.T) {
	ins := provider.Instance{Type: provider.TypeOpencode, Name: "x", OpencodeConfig: &provider.OpencodeConfig{DataDir: t.TempDir()},
		ExtraMCPServers: `{"wick":{"url":"https://evil"}}`}
	if _, err := spawnEnv(ins, "", "", "", nil); err == nil {
		t.Fatal("extra named wick accepted")
	}
}

func TestConfigWithoutMCP(t *testing.T) {
	if c := configContent(false, "", nil, nil); strings.Contains(c, "mcp") || !strings.Contains(c, `"share":"disabled"`) {
		t.Fatal(c)
	}
}

func TestForeignMCPNames(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "repo", "sub")
	home := filepath.Join(root, "home")
	os.MkdirAll(filepath.Join(ws, ".opencode"), 0o755)
	os.MkdirAll(filepath.Join(home, ".opencode"), 0o755)
	os.WriteFile(filepath.Join(root, "repo", "opencode.jsonc"), []byte("{\n // comment\n \"mcp\": {\"projmcp\": {\"type\": \"remote\", \"url\": \"http://x//y\"},},\n}"), 0o644)
	os.WriteFile(filepath.Join(ws, ".opencode", "opencode.json"), []byte(`{"mcp":{"dotmcp":{}}}`), 0o644)
	os.WriteFile(filepath.Join(home, ".opencode", "opencode.json"), []byte(`{"mcp":{"homemcp":{}}}`), 0o644)
	got := foreignMCPNames(ws, home)
	if strings.Join(got, ",") != "dotmcp,homemcp,projmcp" {
		t.Fatalf("got %v", got)
	}
}

func TestResolveModel(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	ins := provider.Instance{Type: provider.TypeOpencode, Name: "oc", OpencodeConfig: &provider.OpencodeConfig{DataDir: data}}
	var live []provider.ModelSeed
	var liveErr error
	orig := listModels
	listModels = func(context.Context, provider.Instance) ([]provider.ModelSeed, error) { return live, liveErr }
	t.Cleanup(func() { listModels = orig })
	login := func(body string) {
		os.MkdirAll(filepath.Join(data, "opencode"), 0o700)
		os.WriteFile(filepath.Join(data, "opencode", "auth.json"), []byte(body), 0o600)
	}
	// no model, no login → refuse, mention login; free Zen models don't count
	live = []provider.ModelSeed{{ID: "opencode/big-pickle"}}
	if _, _, err := resolveModel(ctx, ins, provider.SpawnOptions{}, nil); !errors.Is(err, ErrNoModel) || !strings.Contains(err.Error(), "log in first") {
		t.Fatalf("got %v", err)
	}
	// logged in to openai → first openai model from the live list
	login(`{"openai":{"type":"oauth"}}`)
	live = []provider.ModelSeed{{ID: "opencode/big-pickle"}, {ID: "anthropic/claude-x"}, {ID: "openai/gpt-5.5"}, {ID: "openai/o5"}}
	if m, _, err := resolveModel(ctx, ins, provider.SpawnOptions{}, nil); err != nil || m != "openai/gpt-5.5" {
		t.Fatalf("login fallback: %q %v", m, err)
	}
	// logged in but the live list has nothing for that provider → refuse
	live = []provider.ModelSeed{{ID: "opencode/big-pickle"}}
	if _, _, err := resolveModel(ctx, ins, provider.SpawnOptions{}, nil); !errors.Is(err, ErrNoModel) || !strings.Contains(err.Error(), "openai") {
		t.Fatalf("got %v", err)
	}
	liveErr = errors.New("boom")
	if _, _, err := resolveModel(ctx, ins, provider.SpawnOptions{}, nil); !errors.Is(err, ErrNoModel) {
		t.Fatalf("list failure: %v", err)
	}
	liveErr = nil
	// curated Models list → its first entry is the instance default
	ins.ModelSelect = true
	ins.Models = []provider.ModelEntry{{ID: " "}, {ID: "openai/o5"}, {ID: "openai/gpt-5.5"}}
	if m, _, err := resolveModel(ctx, ins, provider.SpawnOptions{}, nil); err != nil || m != "openai/o5" {
		t.Fatalf("models default: %q %v", m, err)
	}
	// opencode_model beats the curated list
	ins.OpencodeConfig.Model = "openai/gpt-5.5"
	if m, _, err := resolveModel(ctx, ins, provider.SpawnOptions{}, nil); err != nil || m != "openai/gpt-5.5" {
		t.Fatalf("instance model: %q %v", m, err)
	}
	// session pin wins over the instance default
	if m, _, _ := resolveModel(ctx, ins, provider.SpawnOptions{ModelID: "openai/o5"}, nil); m != "openai/o5" {
		t.Fatalf("pin: %q", m)
	}
	// hosted opencode/… and opencode-go/… refused unless allowed
	for _, h := range []string{"opencode/big-pickle", "opencode-go/kimi"} {
		if _, _, err := resolveModel(ctx, ins, provider.SpawnOptions{}, []string{"--model", h}); err == nil || !strings.Contains(err.Error(), "opencode_allow_hosted") {
			t.Fatalf("%s allowed by default: %v", h, err)
		}
	}
	if _, _, err := resolveModel(ctx, ins, provider.SpawnOptions{ModelID: "opencode/big-pickle"}, nil); err == nil {
		t.Fatal("hosted pin allowed by default")
	}
	ins.OpencodeConfig.AllowHosted = true
	if m, inArgs, err := resolveModel(ctx, ins, provider.SpawnOptions{}, []string{"--model=opencode/big-pickle"}); err != nil || m != "opencode/big-pickle" || !inArgs {
		t.Fatalf("hosted opt-in: %q %v %v", m, inArgs, err)
	}
}

func TestResolveModelZenLogin(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	ins := provider.Instance{Type: provider.TypeOpencode, Name: "oc", OpencodeConfig: &provider.OpencodeConfig{DataDir: data}}
	orig := listModels
	live := []provider.ModelSeed{{ID: "opencode/big-pickle"}, {ID: "opencode-go/kimi"}, {ID: "openai/gpt-5.5"}}
	listModels = func(context.Context, provider.Instance) ([]provider.ModelSeed, error) { return live, nil }
	t.Cleanup(func() { listModels = orig })
	// logged in to Zen itself → hosted models allowed without allow_hosted
	os.MkdirAll(filepath.Join(data, "opencode"), 0o700)
	os.WriteFile(filepath.Join(data, "opencode", "auth.json"), []byte(`{"opencode":{"type":"api"}}`), 0o600)
	if m, _, err := resolveModel(ctx, ins, provider.SpawnOptions{}, nil); err != nil || m != "opencode/big-pickle" {
		t.Fatalf("zen login: %q %v", m, err)
	}
	if m, _, err := resolveModel(ctx, ins, provider.SpawnOptions{ModelID: "opencode-go/kimi"}, nil); err != nil || m != "opencode-go/kimi" {
		t.Fatalf("zen pin: %q %v", m, err)
	}
	// Zen + openai → the non-hosted provider wins the fallback
	os.WriteFile(filepath.Join(data, "opencode", "auth.json"), []byte(`{"opencode":{},"openai":{}}`), 0o600)
	if m, _, err := resolveModel(ctx, ins, provider.SpawnOptions{}, nil); err != nil || m != "openai/gpt-5.5" {
		t.Fatalf("mixed login: %q %v", m, err)
	}
	// OPENCODE_API_KEY in the instance env counts as a Zen login
	os.Remove(filepath.Join(data, "opencode", "auth.json"))
	ins.Env = []string{"OPENCODE_API_KEY=k"}
	if m, _, err := resolveModel(ctx, ins, provider.SpawnOptions{}, nil); err != nil || m != "opencode/big-pickle" {
		t.Fatalf("env key: %q %v", m, err)
	}
}

// Live mode: the resolved --model is the live pin when the CLI still lists
// it, else the first model the live filter matches — ahead of opencode_model.
func TestResolveModelLiveDefault(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "opencode")
	os.WriteFile(bin, []byte("#!/bin/sh\nprintf 'opencode/big-pickle\\nopenai/gpt-5.5-mini\\nopenai/gpt-5.5\\nanthropic/claude-sonnet\\n'\n"), 0o755)
	data := t.TempDir()
	os.MkdirAll(filepath.Join(data, "opencode"), 0o700)
	os.WriteFile(filepath.Join(data, "opencode", "auth.json"), []byte(`{"openai":{"type":"oauth"}}`), 0o600)
	ins := provider.Instance{
		Type: provider.TypeOpencode, Name: "oc-live-" + filepath.Base(dir), Binary: bin,
		LiveModels: true, LiveModelFilter: "gpt|claude !mini",
		OpencodeConfig: &provider.OpencodeConfig{DataDir: data, Model: "openai/o5"},
	}
	ctx := context.Background()
	// Nothing known yet and no running server: no CLI run for a default,
	// the configured opencode_model applies.
	if m, _, err := resolveModel(ctx, ins, provider.SpawnOptions{}, nil); err != nil || m != "openai/o5" {
		t.Fatalf("cold live list: %q %v", m, err)
	}
	// After the user's Refresh the live list decides.
	if _, _, err := provider.CachedCLIModels(ctx, ins, true); err != nil {
		t.Fatal(err)
	}
	if m, _, err := resolveModel(ctx, ins, provider.SpawnOptions{}, nil); err != nil || m != "openai/gpt-5.5" {
		t.Fatalf("first live match: %q %v", m, err)
	}
	ins.LiveModelDefault = "anthropic/claude-sonnet"
	if m, _, err := resolveModel(ctx, ins, provider.SpawnOptions{}, nil); err != nil || m != "anthropic/claude-sonnet" {
		t.Fatalf("live pin: %q %v", m, err)
	}
	// a session pin still wins
	if m, _, _ := resolveModel(ctx, ins, provider.SpawnOptions{ModelID: "openai/o6"}, nil); m != "openai/o6" {
		t.Fatalf("session pin: %q", m)
	}
	// nothing matches → falls back to opencode_model
	ins.LiveModelFilter, ins.LiveModelDefault = "nomatch", ""
	if m, _, err := resolveModel(ctx, ins, provider.SpawnOptions{}, nil); err != nil || m != "openai/o5" {
		t.Fatalf("fallback: %q %v", m, err)
	}
}

func TestUseServerAndSkillsEnv(t *testing.T) {
	ins := provider.Instance{Type: provider.TypeOpencode, Name: "oc"}
	if !useServer(ins, []string{"--model", "a/b"}, []string{"--model=a/b"}) {
		t.Fatal("server mode must be the default")
	}
	if useServer(ins, []string{"--agent", "plan"}) {
		t.Fatal("a run-only flag must fall back to opencode run")
	}
	ins.OpencodeConfig = &provider.OpencodeConfig{DataDir: t.TempDir()}
	ins.RunPerTurn = true
	if useServer(ins) {
		t.Fatal("RunPerTurn must use opencode run")
	}
	if serverIdle(ins, provider.SpawnOptions{}) != DefaultServerIdle {
		t.Fatal("idle default")
	}
	if serverIdle(ins, provider.SpawnOptions{IdleTimeout: 2 * time.Minute}) != 2*time.Minute {
		t.Fatal("idle must follow the pool idle timeout")
	}
	ins.ServerIdleMinutes = 7
	if serverIdle(ins, provider.SpawnOptions{IdleTimeout: 2 * time.Minute}) != 7*time.Minute {
		t.Fatal("the instance's own idle window wins")
	}
	ins.ServerIdleMinutes = 0
	has := func(env []string, kv string) bool {
		for _, e := range env {
			if e == kv {
				return true
			}
		}
		return false
	}
	env, err := spawnEnv(ins, "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !has(env, "OPENCODE_DISABLE_CLAUDE_CODE_SKILLS=1") || !has(env, "OPENCODE_DISABLE_EXTERNAL_SKILLS=1") {
		t.Fatalf("external skills not disabled by default: %v", env)
	}
	ins.LoadExternalSkills = true
	env, _ = spawnEnv(ins, "", "", "", nil)
	if has(env, "OPENCODE_DISABLE_CLAUDE_CODE_SKILLS=1") {
		t.Fatal("LoadExternalSkills still disables the scan")
	}
}

// opencode rows of the live-model table: every turn names a model, and it
// is the TARGET instance's — a session copied in from another instance
// runs on this one's live default, not the model its messages recorded.
func TestResolveModelLiveTable(t *testing.T) {
	t.Cleanup(provider.SetModelStateDirForTest(t.TempDir()))
	ctx := context.Background()
	mk := func(name, def string) provider.Instance {
		return provider.Instance{Type: provider.TypeOpencode, Name: name, LiveModels: true, LiveModelDefault: def,
			OpencodeConfig: &provider.OpencodeConfig{DataDir: t.TempDir(), AllowHosted: true}}
	}
	b := mk("oc-live-b", "opencode/nemotron-free")
	t.Cleanup(provider.SetCLIModelsForTest(b, "opencode/mimo-free", "opencode/nemotron-free"))
	noDef := mk("oc-live-nodef", "")
	t.Cleanup(provider.SetCLIModelsForTest(noDef, "opencode/mimo-free", "opencode/nemotron-free"))

	rows := []struct {
		name string
		ins  provider.Instance
		opt  provider.SpawnOptions
		args []string
		want string
	}{
		{"session pin wins", b, provider.SpawnOptions{ModelID: "opencode/mimo-free"}, nil, "opencode/mimo-free"},
		{"--model in args wins", b, provider.SpawnOptions{}, []string{"--model", "opencode/x-free"}, "opencode/x-free"},
		{"live + chosen Default model", b, provider.SpawnOptions{}, nil, "opencode/nemotron-free"},
		{"resumed session from another instance: still B's default", b, provider.SpawnOptions{ResumeID: "ses_1"}, nil, "opencode/nemotron-free"},
		{"live, no Default: first live model", noDef, provider.SpawnOptions{}, nil, "opencode/mimo-free"},
	}
	for _, r := range rows {
		r.opt.Instance = &r.ins
		m, _, err := resolveModel(ctx, r.ins, r.opt, r.args)
		if err != nil || m != r.want {
			t.Errorf("%s: model %q err %v, want %q", r.name, m, err, r.want)
		}
	}
	// The chosen Default refused on this account: the next usable model.
	provider.MarkModelUnavailable(b, "opencode", "opencode/nemotron-free", "model_not_found")
	if m, _, err := resolveModel(ctx, b, provider.SpawnOptions{Instance: &b}, nil); err != nil || m != "opencode/mimo-free" {
		t.Errorf("refused default: %q %v", m, err)
	}
}
