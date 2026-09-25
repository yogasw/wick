package aimemory

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/agentmemory"
	"github.com/yogasw/wick/internal/agents/provider"
)

func TestRegisteredDescriptorMatchesVerifiedFacts(t *testing.T) {
	be, ok := agentmemory.Get("ai-memory")
	if !ok {
		t.Fatal("ai-memory not registered")
	}
	if be.Desc.PrefPort != 49374 {
		t.Fatalf("pref port = %d, want 49374", be.Desc.PrefPort)
	}
	if be.Desc.HealthPath != "/healthz" {
		t.Fatalf("health path = %q, want /healthz (every other path 404s)", be.Desc.HealthPath)
	}
	if be.Desc.Hook == nil {
		t.Fatal("ai-memory must be usable as a spawn target")
	}
}

func TestLaunchBuildsVerifiedServeCommand(t *testing.T) {
	args, _ := launch(agentmemory.LaunchOptions{Port: 49374})
	if got := strings.Join(args, " "); got != "serve --transport http --bind 127.0.0.1:49374" {
		t.Fatalf("launch args = %q", got)
	}

	// --data-dir is a global flag: before the subcommand, not after it.
	args, _ = launch(agentmemory.LaunchOptions{Port: 50000, DataDir: "/srv/mem"})
	if got := strings.Join(args, " "); got != "--data-dir /srv/mem serve --transport http --bind 127.0.0.1:50000" {
		t.Fatalf("launch args with data dir = %q", got)
	}

	// The backend's own web UI is opt-in, off by default.
	args, _ = launch(agentmemory.LaunchOptions{Port: 49374})
	if strings.Contains(strings.Join(args, " "), "--enable-web") {
		t.Fatal("web UI must be off unless opted in")
	}
	args, _ = launch(agentmemory.LaunchOptions{Port: 49374, EnableWeb: true})
	if !strings.Contains(strings.Join(args, " "), "--enable-web") {
		t.Fatal("--enable-web not added when opted in")
	}
}

func TestContributeClaudeInlineMCPConfig(t *testing.T) {
	args, env, err := hook{}.Contribute(provider.TypeClaude, provider.Instance{}, agentmemory.SpawnConn{ServerURL: "http://127.0.0.1:49374", AuthKey: ""})
	if err != nil {
		t.Fatalf("contribute err: %v", err)
	}
	if len(args) < 2 || args[0] != "--mcp-config" {
		t.Fatalf("claude args should lead with --mcp-config: %v", args)
	}

	var cfg struct {
		MCPServers map[string]struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(args[1]), &cfg); err != nil {
		t.Fatalf("inline mcp config is not valid JSON: %v (%q)", err, args[1])
	}
	srv, ok := cfg.MCPServers["ai-memory"]
	if !ok {
		t.Fatalf("mcp server entry missing: %q", args[1])
	}
	if srv.Type != "http" || srv.URL != "http://127.0.0.1:49374/mcp" {
		t.Fatalf("mcp server wired wrong: %+v", srv)
	}

	// The contribution must NOT isolate MCP. wick dropped --strict-mcp-config
	// on purpose: it never restricted wick's own tools, it only dropped the
	// USER'S other MCP servers — see TestArgvNeverIsolatesMCP in
	// internal/agents/provider/claude. Turning memory on must not unplug
	// someone's unrelated tools as a side effect.
	for _, a := range args {
		if a == "--strict-mcp-config" {
			t.Fatalf("contribution isolates MCP; wick merges with the user's own servers: %v", args)
		}
	}
	if joined := strings.Join(env, " "); !strings.Contains(joined, "AI_MEMORY_SERVER_URL=http://127.0.0.1:49374") {
		t.Fatalf("server env missing: %v", env)
	}
	if strings.Contains(strings.Join(env, " "), "AI_MEMORY_AUTH_TOKEN") {
		t.Fatalf("no auth token configured, none should be passed: %v", env)
	}
}

// captureConn is the wiring the verified `install-hooks` capture was taken
// with, so a test can compare against that output line for line.
var captureConn = agentmemory.SpawnConn{
	ServerURL: "http://127.0.0.1:49374",
	BinPath:   "/opt/ai-memory/ai-memory",
	DataDir:   "/srv/aim/data",
}

// hookGroup mirrors one entry of claude's per-event hook list.
type hookGroup struct {
	Matcher string `json:"matcher"`
	Hooks   []struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	} `json:"hooks"`
}

// parseHooks decodes a --settings payload back into claude's hooks shape.
func parseHooks(t *testing.T, payload string) map[string][]hookGroup {
	t.Helper()
	var got struct {
		Hooks map[string][]hookGroup `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("settings payload is not valid JSON: %v (%q)", err, payload)
	}
	return got.Hooks
}

// TestCaptureSettingsMatchesInstallHooksOutput pins the generated block to the
// real thing: `ai-memory --data-dir <dir> install-hooks --agent claude-code`
// on ai-memory 2.4.0, captured 2026-09-25. All nine events, each an empty
// matcher running a NATIVE `ai-memory hook` command — no shell script, which
// is what lets wick generate this per spawn instead of writing a file.
func TestCaptureSettingsMatchesInstallHooksOutput(t *testing.T) {
	hooks := parseHooks(t, captureSettings(captureConn))

	want := map[string]string{
		"SessionStart":     "session-start",
		"UserPromptSubmit": "user-prompt-submit",
		"PreToolUse":       "pre-tool-use",
		"PostToolUse":      "post-tool-use",
		"PreCompact":       "pre-compact",
		"Stop":             "stop",
		"SessionEnd":       "session-end",
		"SubagentStart":    "subagent-start",
		"SubagentStop":     "subagent-stop",
	}
	if len(hooks) != len(want) {
		t.Fatalf("hook events = %d, want %d: %v", len(hooks), len(want), hooks)
	}
	for event, slug := range want {
		groups, ok := hooks[event]
		if !ok {
			t.Fatalf("event %q missing; the capture installs all nine", event)
		}
		if len(groups) != 1 || groups[0].Matcher != "" || len(groups[0].Hooks) != 1 {
			t.Fatalf("event %q shape = %+v, want one empty-matcher group with one command", event, groups)
		}
		cmd := groups[0].Hooks[0]
		if cmd.Type != "command" {
			t.Fatalf("event %q hook type = %q, want command", event, cmd.Type)
		}
		wantCmd := "/opt/ai-memory/ai-memory --data-dir /srv/aim/data hook" +
			" --event " + slug + " --agent claude-code --server-url http://127.0.0.1:49374"
		if cmd.Command != wantCmd {
			t.Fatalf("event %q command =\n %q\nwant\n %q", event, cmd.Command, wantCmd)
		}
	}
}

// The store belongs to the daemon, and a daemon on the backend's own default
// has no path to pass on — the hook must inherit that default rather than be
// pinned to a directory wick never chose.
func TestCaptureSettingsOmitsDataDirWhenDaemonUsesDefault(t *testing.T) {
	conn := captureConn
	conn.DataDir = ""
	cmd := parseHooks(t, captureSettings(conn))["SessionStart"][0].Hooks[0].Command
	if strings.Contains(cmd, "--data-dir") {
		t.Fatalf("default store must not be pinned: %q", cmd)
	}
	if !strings.HasPrefix(cmd, "/opt/ai-memory/ai-memory hook --event session-start ") {
		t.Fatalf("command = %q", cmd)
	}
}

// A hook line is run by a shell. An unquoted path with a space would split
// into two arguments and fail on every single tool call.
func TestCaptureSettingsQuotesAwkwardPaths(t *testing.T) {
	hooks := parseHooks(t, captureSettings(agentmemory.SpawnConn{
		ServerURL: "http://127.0.0.1:49374",
		BinPath:   "/opt/my tools/ai-memory",
		DataDir:   "/srv/aim data",
	}))
	cmd := hooks["PostToolUse"][0].Hooks[0].Command
	if !strings.HasPrefix(cmd, "'/opt/my tools/ai-memory' --data-dir '/srv/aim data' hook ") {
		t.Fatalf("awkward paths not shell-safe: %q", cmd)
	}
}

// Without a resolvable binary there is no command to run, and installing one
// anyway would print a spawn failure on every tool call. Recall (MCP) still
// has to work, so this must degrade to "read-only", not to a broken session.
func TestCaptureSettingsEmptyWithoutBinary(t *testing.T) {
	conn := captureConn
	conn.BinPath = ""
	if s := captureSettings(conn); s != "" {
		t.Fatalf("no binary resolved, want no --settings: %q", s)
	}
}

func TestContributeClaudeCaptureInstallsHooksFirst(t *testing.T) {
	args, _, err := hook{}.Contribute(provider.TypeClaude,
		provider.Instance{AgentMemoryCapture: true}, captureConn)
	if err != nil {
		t.Fatalf("contribute err: %v", err)
	}
	// Capture must not smuggle the isolation flag back in either.
	for _, a := range args {
		if a == "--strict-mcp-config" {
			t.Fatalf("capture path isolates MCP: %v", args)
		}
	}
	// --settings leads and the variadic --mcp-config trails: the other order
	// would feed the settings block to --mcp-config, which reads every value
	// that follows it.
	if len(args) != 4 || args[0] != "--settings" || args[2] != "--mcp-config" {
		t.Fatalf("capture arg order wrong: %v", args)
	}
	if parseHooks(t, args[1])["PreToolUse"] == nil {
		t.Fatalf("settings payload carries no hooks: %q", args[1])
	}

	// Capture is the opt-in half. MCP (recall) is wired whenever memory is on;
	// hooks (recording) only when the instance asks, because PreToolUse and
	// PostToolUse fire on every single tool call and that costs.
	off, _, err := hook{}.Contribute(provider.TypeClaude, provider.Instance{}, captureConn)
	if err != nil {
		t.Fatalf("contribute err: %v", err)
	}
	for _, a := range off {
		if a == "--settings" {
			t.Fatalf("capture off, hooks must not be installed: %v", off)
		}
	}
}

// codex gets its own first-party hook path (PLAN §12.1); until that is wired,
// claude's settings flag must not leak into its argv.
func TestContributeCodexCaptureAddsNoClaudeSettings(t *testing.T) {
	args, _, err := hook{}.Contribute(provider.TypeCodex,
		provider.Instance{AgentMemoryCapture: true}, captureConn)
	if err != nil {
		t.Fatalf("contribute err: %v", err)
	}
	for _, a := range args {
		if a == "--settings" {
			t.Fatalf("claude-only flag reached codex: %v", args)
		}
	}
}

func TestContributeCodexTomlOverrides(t *testing.T) {
	args, env, err := hook{}.Contribute(provider.TypeCodex, provider.Instance{}, agentmemory.SpawnConn{ServerURL: "http://127.0.0.1:49374", AuthKey: "tok"})
	if err != nil {
		t.Fatalf("contribute err: %v", err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, `-c mcp_servers.ai-memory.url="http://127.0.0.1:49374/mcp"`) {
		t.Fatalf("codex mcp url override missing/unquoted: %v", args)
	}
	if !strings.Contains(joined, `-c mcp_servers.ai-memory.default_tools_approval_mode="approve"`) {
		t.Fatalf("codex approval-mode override missing: %v", args)
	}
	if strings.Contains(joined, "tok") {
		t.Fatalf("auth token must not reach argv: %v", args)
	}
	joinedEnv := strings.Join(env, " ")
	if !strings.Contains(joinedEnv, "AI_MEMORY_AUTH_TOKEN=tok") {
		t.Fatalf("auth token not passed via env: %v", env)
	}
	// The store is a daemon-level setting (PLAN §19) — the child is told the
	// server to talk to, never a store to open.
	if strings.Contains(joinedEnv, "AI_MEMORY_DATA_DIR") {
		t.Fatalf("data dir must not be pushed per-instance: %v", env)
	}
}

func TestContributeUnsupportedTypeIsEmpty(t *testing.T) {
	args, env, err := hook{}.Contribute(provider.TypeGemini, provider.Instance{}, agentmemory.SpawnConn{ServerURL: "http://127.0.0.1:49374", AuthKey: ""})
	if err != nil {
		t.Fatalf("contribute err: %v", err)
	}
	if args != nil || env != nil {
		t.Fatalf("gemini has no per-spawn path yet, want nothing: args=%v env=%v", args, env)
	}
}
