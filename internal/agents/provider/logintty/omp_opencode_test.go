package logintty

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/provider"
)

func TestOMPLoginUsesInstanceProfile(t *testing.T) {
	ins := provider.Instance{Type: provider.TypeOMP, Name: "renamed", OMPConfig: &provider.OMPConfig{Profile: "wick-first"}}
	args, ok := LoginCommandFor(ins, "anthropic")
	if !ok || !slices.Equal(args, []string{"--profile", "wick-first", "login", "anthropic"}) {
		t.Fatalf("args = %q", args)
	}
	// Unknown choice never reaches argv.
	args, _ = LoginCommandFor(ins, "--evil")
	if args[3] != "openai-codex-device" {
		t.Fatalf("fallback = %q", args)
	}
	// Same profile the spawner uses.
	if args[1] != provider.OMPProfile(ins) {
		t.Fatal("login/spawn profile mismatch")
	}
}

func TestAccountEnvDropsConflictingKeys(t *testing.T) {
	ins := provider.Instance{Type: provider.TypeOMP, Name: "o", Env: []string{"OMP_PROFILE=other", "FOO=1"}, OMPConfig: &provider.OMPConfig{Profile: "wick-o"}}
	env := provider.AccountEnv(ins)
	if !slices.Equal(env, []string{"FOO=1", "OMP_PROFILE=wick-o"}) {
		t.Fatalf("env = %q", env)
	}
	if got := ompProfileFromEnv(env); got != "wick-o" {
		t.Fatal(got)
	}
}

func TestOpencodeLoginCommand(t *testing.T) {
	cases := map[string][]string{
		"":                {"auth", "login", "-p", "openai", "-m", "ChatGPT Pro/Plus (headless)"},
		"openai-headless": {"auth", "login", "-p", "openai", "-m", "ChatGPT Pro/Plus (headless)"},
		"pick":            {"auth", "login"},
		"openrouter":      {"auth", "login", "-p", "openrouter"},
		"anthropic":       {"auth", "login", "-p", "openai", "-m", "ChatGPT Pro/Plus (headless)"},
		"-x; rm":          {"auth", "login", "-p", "openai", "-m", "ChatGPT Pro/Plus (headless)"},
	}
	for in, want := range cases {
		if got := opencodeLoginCommand(provider.Instance{Type: provider.TypeOpencode, Name: "oc"}, in); !slices.Equal(got, want) {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestOpencodeAccountFromInstanceDir(t *testing.T) {
	dir := t.TempDir()
	ins := provider.Instance{Type: provider.TypeOpencode, Name: "oc", OpencodeConfig: &provider.OpencodeConfig{DataDir: dir}}
	env := provider.AccountEnv(ins)
	if ReadAccount(provider.TypeOpencode, env).Connected {
		t.Fatal("empty dir reported connected")
	}
	os.MkdirAll(filepath.Join(dir, "opencode"), 0o700)
	os.WriteFile(filepath.Join(dir, "opencode", "auth.json"),
		[]byte(`{"openai":{"type":"oauth","refresh":"r","access":"a","expires":1790000000000,"accountId":"acc_1"}}`), 0o600)
	acc := ReadAccount(provider.TypeOpencode, env)
	if !acc.Connected || acc.Plan != "openai" || acc.AuthMethod != "OAuth" || acc.Org != "acc_1" {
		t.Fatalf("acc = %+v", acc)
	}
	if ConfigDir(provider.TypeOpencode, env) != filepath.Join(dir, "opencode") {
		t.Fatal("config dir")
	}
}

const ompUsageFixture = `{"generatedAt":1,"reports":[{"provider":"openai-codex","fetchedAt":1,
"limits":[{"id":"primary","label":"5 Hour","scope":{"provider":"openai-codex"},"window":{"id":"5h","label":"5 Hour","durationMs":18000000,"resetsAt":1790000000000},"amount":{"usedFraction":0.42,"unit":"percent"}},
{"id":"secondary","label":"7 Day","scope":{"provider":"openai-codex"},"window":{"id":"7d","label":"7 Day","durationMs":604800000},"amount":{"used":30,"limit":100,"unit":"percent"}}],
"metadata":{"email":"a@example.com","accountId":"x"}}],"accountsWithoutUsage":[],"disabledCredentials":[],"capacity":{}}`

func TestOMPUsageAndAccount(t *testing.T) {
	old := ompRunner
	t.Cleanup(func() { ompRunner = old })
	var gotEnv []string
	ompRunner = func(_ context.Context, env []string) ([]byte, error) {
		gotEnv = env
		return []byte("banner\n" + ompUsageFixture), nil
	}
	ins := provider.Instance{Type: provider.TypeOMP, Name: "u", OMPConfig: &provider.OMPConfig{Profile: "wick-usage-test"}}
	env := provider.AccountEnv(ins)
	ws, err := ReadUsage(provider.TypeOMP, env)
	if err != nil || len(ws) != 2 {
		t.Fatalf("windows %v %v", ws, err)
	}
	if ws[0].Key != "five_hour" || ws[0].Utilization != 42 || ws[0].ResetsAt.IsZero() {
		t.Errorf("5h = %+v", ws[0])
	}
	if ws[1].Key != "seven_day" || ws[1].Utilization != 30 {
		t.Errorf("7d = %+v", ws[1])
	}
	acc := ReadAccount(provider.TypeOMP, env)
	if !acc.Connected || acc.Email != "a@example.com" || acc.AuthMethod != "openai-codex" || acc.Plan != "" {
		t.Fatalf("acc = %+v", acc)
	}
	if !slices.Contains(gotEnv, "OMP_PROFILE=wick-usage-test") {
		t.Fatalf("usage ran without the instance profile: %q", gotEnv)
	}
	if !SupportsUsage(provider.TypeOMP) || !SupportsUsage(provider.TypeOpencode) || SupportsUsage(provider.TypeGemini) {
		t.Fatal("usage support flags")
	}
	if !strings.HasSuffix(ConfigDir(provider.TypeOMP, env), filepath.Join("profiles", "wick-usage-test", "agent")) {
		t.Fatal(ConfigDir(provider.TypeOMP, env))
	}
}

func TestLoginChoices(t *testing.T) {
	var warned bool
	for _, c := range LoginChoices(provider.Instance{Type: provider.TypeOMP, Name: "omp"}) {
		if c.ID == "anthropic" && c.Warning != "" {
			warned = true
		}
	}
	if !warned {
		t.Fatal("anthropic must carry a policy warning")
	}
	if LoginChoices(provider.Instance{Type: provider.TypeClaude}) != nil {
		t.Fatal("claude has no picker")
	}
	if !strings.Contains(LoginNote(provider.TypeOpencode), "Claude") {
		t.Fatal("opencode note")
	}
}
