package terminal

import (
	"slices"
	"strings"
	"testing"

	provider "github.com/yogasw/wick/internal/agents/provider"
)

func TestCommandsAllowlist(t *testing.T) {
	omp := provider.Instance{Type: provider.TypeOMP, Name: "work"}
	var labels []string
	for _, c := range Commands(omp) {
		labels = append(labels, c.Label)
	}
	p := provider.OMPProfile(omp)
	want := []string{"omp --profile " + p, "omp login", "omp usage"}
	if !slices.Equal(labels, want) {
		t.Fatalf("omp labels = %v, want %v", labels, want)
	}
	if c, ok := LookupCommand(omp, "tui"); !ok || !slices.Equal(c.Args, []string{"--profile", p}) {
		t.Fatalf("omp tui = %+v", c)
	}

	oc := provider.Instance{Type: provider.TypeOpencode, Name: "x"}
	c, ok := LookupCommand(oc, "auth-list")
	if !ok || !slices.Equal(c.Args, []string{"auth", "list"}) {
		t.Fatalf("opencode auth-list = %+v", c)
	}
	if c, ok := LookupCommand(oc, "tui"); !ok || len(c.Args) != 0 {
		t.Fatalf("opencode tui = %+v", c)
	}
	for _, bad := range []string{"", "bash", "auth list", "../tui", "tui;id"} {
		if _, ok := LookupCommand(oc, bad); ok {
			t.Errorf("%q must not be in the allowlist", bad)
		}
	}
	if Commands(provider.Instance{Type: provider.TypeWick}) != nil {
		t.Error("wick has no web terminal")
	}
}

func TestCommandsCLITypes(t *testing.T) {
	cases := []struct {
		typ  provider.Type
		keys []string
		args map[string][]string
	}{
		{provider.TypeClaude, []string{"tui", "auth-login", "auth-status"}, map[string][]string{
			"auth-login": {"auth", "login"}, "auth-status": {"auth", "status"}}},
		{provider.TypeCodex, []string{"tui", "login", "login-status"}, map[string][]string{
			"login": {"login", "--device-auth"}, "login-status": {"login", "status"}}},
		{provider.TypeGemini, []string{"tui"}, nil},
	}
	for _, tc := range cases {
		ins := provider.Instance{Type: tc.typ, Name: "x"}
		var keys []string
		for _, c := range Commands(ins) {
			keys = append(keys, c.Key)
		}
		if !slices.Equal(keys, tc.keys) {
			t.Errorf("%s keys = %v, want %v", tc.typ, keys, tc.keys)
		}
		if c, ok := LookupCommand(ins, "tui"); !ok || len(c.Args) != 0 || c.Label != string(tc.typ) {
			t.Errorf("%s tui = %+v", tc.typ, c)
		}
		for k, want := range tc.args {
			if c, ok := LookupCommand(ins, k); !ok || !slices.Equal(c.Args, want) {
				t.Errorf("%s %s = %+v", tc.typ, k, c)
			}
		}
		for _, bad := range []string{"", "bash", "login status", "../tui", "tui;id", "mcp", "exec"} {
			if _, ok := LookupCommand(ins, bad); ok {
				t.Errorf("%s: %q must not be in the allowlist", tc.typ, bad)
			}
		}
	}
}

func TestEnvCarriesInstanceAccount(t *testing.T) {
	t.Setenv("GOTTY_PERMIT_ARGUMENTS", "true")
	cl := Env(provider.Instance{Type: provider.TypeClaude, Name: "work", Env: []string{"CLAUDE_CONFIG_DIR=/srv/claude-work"}})
	if !slices.Contains(cl, "CLAUDE_CONFIG_DIR=/srv/claude-work") {
		t.Errorf("claude env must carry the instance CLAUDE_CONFIG_DIR: %v", cl)
	}
	cx := Env(provider.Instance{Type: provider.TypeCodex, Name: "work", Env: []string{"CODEX_HOME=/srv/codex-work"}})
	if !slices.Contains(cx, "CODEX_HOME=/srv/codex-work") {
		t.Errorf("codex env must carry the instance CODEX_HOME: %v", cx)
	}
	for _, env := range [][]string{cl, cx} {
		for _, kv := range env {
			if strings.HasPrefix(kv, "GOTTY_") || strings.HasPrefix(kv, provider.AccountBinEnvKey+"=") {
				t.Errorf("%q must be dropped", kv)
			}
		}
	}
}

func TestGottyArgs(t *testing.T) {
	args := GottyArgs(Spec{Port: 40001, ConfigPath: "/cfg/gotty.hcl", BasePath: "/b/t/abc/", Bin: "/bin/opencode", Args: []string{"auth", "list"}})
	got := strings.Join(args, " ")
	for _, want := range []string{
		"--config /cfg/gotty.hcl", "--address 127.0.0.1", "--port 40001", "--path /b/t/abc/",
		"--once", "--permit-write",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("argv %q lacks %q", got, want)
		}
	}
	// The credential is world-readable in /proc/<pid>/cmdline; it lives in
	// the 0600 config file instead.
	for _, never := range []string{"--permit-arguments", "--random-url", "0.0.0.0", "--reconnect", "--credential"} {
		if strings.Contains(got, never) {
			t.Errorf("argv %q must not contain %q", got, never)
		}
	}
	if !strings.HasSuffix(got, " /bin/opencode auth list") {
		t.Errorf("argv must end with the command: %q", got)
	}
}

func TestEnv(t *testing.T) {
	t.Setenv("GOTTY_PERMIT_ARGUMENTS", "true")
	t.Setenv("GOTTY_ADDRESS", "0.0.0.0")
	oc := provider.Instance{Type: provider.TypeOpencode, Name: "x", Env: []string{"XDG_DATA_HOME=/tmp/wick-scratch-oc"}}
	env := Env(oc)
	var has = func(p string) bool {
		return slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, p) })
	}
	if has("GOTTY_") {
		t.Error("GOTTY_* must be dropped (gotty reads flags from env)")
	}
	if !slices.Contains(env, "OPENCODE_CONFIG=") || !slices.Contains(env, "OPENCODE_CONFIG_DIR=") {
		t.Error("opencode config overrides must be blanked")
	}
	if !has("XDG_CONFIG_HOME=") || !has("OPENCODE_DISABLE_AUTOUPDATE=true") {
		t.Errorf("opencode instance env missing: %v", env)
	}
	if has(provider.AccountBinEnvKey + "=") {
		t.Error("wick-internal account bin var must not leak into the terminal")
	}
	omp := Env(provider.Instance{Type: provider.TypeOMP, Name: "w"})
	if !slices.Contains(omp, "OMP_PROFILE="+provider.OMPProfile(provider.Instance{Type: provider.TypeOMP, Name: "w"})) {
		t.Error("omp env must pin OMP_PROFILE")
	}
}

func TestGottyConfigCarriesTheCredential(t *testing.T) {
	got := string(GottyConfig(`u:p"x`))
	if !strings.Contains(got, "enable_basic_auth = true") || !strings.Contains(got, `credential = "u:p\"x"`) {
		t.Fatalf("config %q", got)
	}
}
