package aimemory

import (
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/agentmemory"
	"github.com/yogasw/wick/internal/agents/provider"
)

func envMap(t *testing.T, env []string) map[string]string {
	t.Helper()
	m := map[string]string{}
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			t.Fatalf("malformed env entry %q", e)
		}
		m[k] = v
	}
	return m
}

// The rule the whole mapping rests on: an UNSET field is not sent. If wick
// sent zeros for everything the operator never touched, turning the panel on
// would silently overwrite a hand-written config.toml with defaults — and for
// observation_retention_days that means quietly disabling a prune the operator
// had configured.
func TestTuningEnvSendsNothingForAnUntouchedForm(t *testing.T) {
	if env := tuningEnv(agentmemory.Tuning{}, ""); len(env) != 0 {
		t.Fatalf("a zero Tuning must produce no environment at all, got %v", env)
	}
}

func TestTuningEnvNestedTablesUseDoubleUnderscore(t *testing.T) {
	env := envMap(t, tuningEnv(agentmemory.Tuning{
		LogLevel:                 "debug",
		AllowedHosts:             " localhost , homelab ",
		ObservationRetentionDays: 90,
		HardDeleteAfterDays:      180,
		ColdThreshold:            0.2,
		MaxInputTokens:           24000,
		AutoImproveMinConfidence: 0.75,
		SanitizeExtraPatterns:    "PROJ-[0-9a-f]{32}\n\n  SECRET-\\d+  ",
		HookRatePerSec:           5,
	}, "tok"))

	want := map[string]string{
		"AI_MEMORY_LOG_LEVEL":                         "debug",
		"AI_MEMORY_ALLOWED_HOSTS":                     "localhost,homelab",
		"AI_MEMORY_AUTH_TOKEN":                        "tok",
		"AI_MEMORY_DECAY__OBSERVATION_RETENTION_DAYS": "90",
		"AI_MEMORY_DECAY__HARD_DELETE_AFTER_DAYS":     "180",
		"AI_MEMORY_DECAY__COLD_THRESHOLD":             "0.2",
		"AI_MEMORY_CONSOLIDATION__MAX_INPUT_TOKENS":   "24000",
		"AI_MEMORY_AUTO_IMPROVE__MIN_CONFIDENCE":      "0.75",
		"AI_MEMORY_SANITIZE__EXTRA_PATTERNS":          `PROJ-[0-9a-f]{32},SECRET-\d+`,
		"AI_MEMORY_HOOK_RATE_PER_SEC":                 "5",
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
	// A float must not arrive in exponent form — an operator reading it back
	// out of a process listing should see what they typed.
	if strings.ContainsAny(env["AI_MEMORY_DECAY__COLD_THRESHOLD"], "eE") {
		t.Errorf("float rendered with an exponent: %q", env["AI_MEMORY_DECAY__COLD_THRESHOLD"])
	}
}

// A bool is only ever sent when TRUE. Sending false would pin a setting off
// against a config file that turned it on — the opposite of "leave it alone".
func TestTuningEnvOnlySendsTrueBooleans(t *testing.T) {
	off := envMap(t, tuningEnv(agentmemory.Tuning{CaptureAssistant: false, AutoImproveRequireApproval: false}, ""))
	if _, ok := off["AI_MEMORY_CAPTURE_ASSISTANT"]; ok {
		t.Error("capture_assistant false must not be sent")
	}
	on := envMap(t, tuningEnv(agentmemory.Tuning{CaptureAssistant: true}, ""))
	if on["AI_MEMORY_CAPTURE_ASSISTANT"] != "true" {
		t.Errorf("capture_assistant on must be sent: %v", on)
	}
}

// capture_mode and project_strategy are NOT config keys — they only persist
// through `install-hooks --apply`, which writes the agent's own settings
// files. They must never leak into the daemon's environment as if they were.
func TestTuningEnvHasNoHookOnlySettings(t *testing.T) {
	env := envMap(t, tuningEnv(agentmemory.Tuning{CaptureMode: "allowlist", ProjectStrategy: "repo-root"}, ""))
	for k := range env {
		if strings.Contains(k, "CAPTURE_MODE") || strings.Contains(k, "PROJECT_STRATEGY") {
			t.Errorf("%s is not a config key and must not be set as env", k)
		}
	}
}

func TestLaunchPassesBasePathAndTheTuningEnv(t *testing.T) {
	args, env := launch(agentmemory.LaunchOptions{
		Port: 49374, DataDir: "/srv/mem", EnableWeb: true, AuthToken: "tok",
		Tuning: agentmemory.Tuning{BasePath: "/memory", LogLevel: "warn"},
	})
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--base-path /memory") {
		t.Fatalf("base path missing: %q", joined)
	}
	// The bind is always loopback, so the flag that exists to unlock a
	// non-loopback bind without auth must never appear.
	if strings.Contains(joined, "--allow-insecure-no-auth") {
		t.Fatalf("--allow-insecure-no-auth must be unreachable from settings: %q", joined)
	}
	m := envMap(t, env)
	if m["AI_MEMORY_LOG_LEVEL"] != "warn" || m["AI_MEMORY_AUTH_TOKEN"] != "tok" {
		t.Fatalf("tuning env not applied: %v", m)
	}
}

// ── hook line ────────────────────────────────────────────────────────

func hookSettings(t *testing.T, tun agentmemory.Tuning) string {
	t.Helper()
	ins := provider.Instance{Type: provider.TypeClaude, AgentMemoryCapture: true}
	args, _, err := hook{}.Contribute(provider.TypeClaude, ins, agentmemory.SpawnConn{
		ServerURL: "http://127.0.0.1:49374", BinPath: "/usr/bin/ai-memory", DataDir: "/srv/mem", Tuning: tun,
	})
	if err != nil {
		t.Fatalf("Contribute: %v", err)
	}
	for i, a := range args {
		if a == "--settings" && i+1 < len(args) {
			return args[i+1]
		}
	}
	t.Fatal("no --settings block generated")
	return ""
}

func TestHookLineBakesTheSettingsThatAreNotConfigKeys(t *testing.T) {
	got := hookSettings(t, agentmemory.Tuning{CaptureMode: "allowlist", ProjectStrategy: "repo-root"})
	for _, want := range []string{"--capture-mode allowlist", "--project-strategy repo-root"} {
		if !strings.Contains(got, want) {
			t.Errorf("hook line missing %q", want)
		}
	}
}

// The hook half of the assistant-capture double opt-in belongs to `stop` and
// to nothing else: the flag is what makes that one event carry the assistant's
// text, and bolting it onto every event would be nine chances to get it wrong.
func TestCaptureAssistantRidesOnlyTheStopHook(t *testing.T) {
	got := hookSettings(t, agentmemory.Tuning{CaptureAssistant: true})
	if strings.Count(got, "--capture-assistant") != 1 {
		t.Fatalf("expected exactly one --capture-assistant, got %d in %s", strings.Count(got, "--capture-assistant"), got)
	}
	if !strings.Contains(got, "--event stop --agent claude-code --server-url http://127.0.0.1:49374 --capture-assistant") {
		t.Fatalf("--capture-assistant is not on the stop hook: %s", got)
	}
	if off := hookSettings(t, agentmemory.Tuning{}); strings.Contains(off, "--capture-assistant") {
		t.Fatal("assistant capture must be off unless asked for")
	}
}

// Not capturing prompts means the hook is never installed, so the text never
// reaches the spool or the wire — not that the daemon throws it away later.
func TestNoCapturePromptsDropsThePromptHookEntirely(t *testing.T) {
	on := hookSettings(t, agentmemory.Tuning{})
	if !strings.Contains(on, promptEvent) {
		t.Fatalf("prompts are captured by default: %s", on)
	}
	off := hookSettings(t, agentmemory.Tuning{NoCapturePrompts: true})
	if strings.Contains(off, promptEvent) || strings.Contains(off, "user-prompt-submit") {
		t.Fatalf("the prompt hook must be absent, not disabled: %s", off)
	}
	// Every other event is untouched — this setting drops one hook, not capture.
	for _, e := range []string{"SessionStart", "PreToolUse", "Stop", "SessionEnd"} {
		if !strings.Contains(off, e) {
			t.Errorf("dropping prompts must not drop %s", e)
		}
	}
}
