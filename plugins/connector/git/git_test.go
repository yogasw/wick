package main

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yogasw/wick/pkg/safeexec"
)

func TestValidateUserArgsRejectsBanned(t *testing.T) {
	banned := [][]string{
		{"-c", "core.pager=sh -c 'id'", "log"},
		{"--config-env=core.pager=EVIL", "log"},
		{"--exec-path=/tmp/evil", "status"},
		{"--upload-pack=/tmp/evil", "fetch"},
		{"--upload-pack", "/tmp/evil", "fetch"},
		{"--receive-pack=/tmp/evil", "push"},
		{"--exec=/tmp/evil", "push"}, // push's alias for --receive-pack
		{"fetch", "ext::sh -c id"},
		{"rebase", "--interactive"},
		{"log", "--output=/etc/passwd"},
	}
	for _, args := range banned {
		if err := ValidateUserArgs(args); err == nil {
			t.Errorf("ValidateUserArgs(%v) = nil, want an error", args)
		}
	}
}

func TestValidateUserArgsAllowsShortFlagsThatAreNotDangerous(t *testing.T) {
	// -u and -i were once banned on the assumption that they were short forms of
	// --upload-pack and --interactive. Verified against git 2.52, they are not:
	// fetch -u is --update-head-ok, push -u is --set-upstream, grep -i is
	// case-insensitive matching. Banning them blocked idiomatic git while
	// stopping nothing, so these must stay allowed.
	ok := [][]string{
		{"push", "-u", "origin", "main"},
		{"branch", "-u", "upstream/main"},
		{"status", "-u"},
		{"status", "-uall"},
		{"stash", "-u"},
		{"grep", "-i", "needle"},
	}
	for _, args := range ok {
		if err := ValidateUserArgs(args); err != nil {
			t.Errorf("ValidateUserArgs(%v) = %v, want nil", args, err)
		}
	}
}

func TestValidateUserArgsAllowsNormal(t *testing.T) {
	ok := [][]string{
		{"status", "--porcelain=v2"},
		{"log", "-n", "20", "--oneline"},
		{"bisect", "start"},
		{"worktree", "list"},
		{"diff", "--stat", "HEAD~1", "HEAD"},
		{"describe", "--tags"},
	}
	for _, args := range ok {
		if err := ValidateUserArgs(args); err != nil {
			t.Errorf("ValidateUserArgs(%v) = %v, want nil", args, err)
		}
	}
}

func TestValidateUserArgsErrorNamesTheFlag(t *testing.T) {
	err := ValidateUserArgs([]string{"-c", "core.pager=x", "log"})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "-c") {
		t.Errorf("error must name the rejected flag, got: %v", err)
	}
}

func TestSplitRawArgs(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`log -n 5`, []string{"log", "-n", "5"}},
		{`commit -m "hello world"`, []string{"commit", "-m", "hello world"}},
		{`commit -m 'single quoted'`, []string{"commit", "-m", "single quoted"}},
		{`  status   --short  `, []string{"status", "--short"}},
		{``, nil},
	}
	for _, c := range cases {
		got := SplitRawArgs(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("SplitRawArgs(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestRawSubcommandOf(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"log", "-n", "5"}, "log"},
		{[]string{"--no-pager", "log"}, "log"}, // global flags skipped
		{[]string{"--git-dir=/x", "status"}, "status"},
		{[]string{"-n", "5"}, ""}, // no subcommand present
		{nil, ""},
	}
	for _, c := range cases {
		if got := RawSubcommandOf(c.in); got != c.want {
			t.Errorf("RawSubcommandOf(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// RawSubcommandOf gates whether raw mode may run, so it must fail closed rather
// than read some flag's value as the subcommand.
func TestRawSubcommandOfFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want string
	}{
		{"separate-value global -C", []string{"-C", "/some/path", "status"}, "status"},
		{"separate-value global -c", []string{"-c", "user.name=x", "log"}, "log"},
		{"boolean global before subcommand", []string{"--no-pager", "--bare", "log"}, "log"},
		{"unknown leading flag is denied", []string{"--frobnicate", "log"}, ""},
		{"value of an unknown flag is not a subcommand", []string{"-n", "5"}, ""},
		{"smuggled subcommand as a flag value", []string{"--depth", "status"}, ""},
		{"only flags, no subcommand", []string{"--no-pager"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := RawSubcommandOf(c.in); got != c.want {
				t.Errorf("RawSubcommandOf(%v) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestArgvOrderPutsInjectedFirst(t *testing.T) {
	c := Cmd{
		RepoPath:     "d:/code/repo",
		InjectedArgs: []string{"-c", "core.hooksPath=/empty"},
		UserArgs:     []string{"commit", "-m", "msg"},
	}
	want := []string{"-c", "core.hooksPath=/empty", "commit", "-m", "msg"}
	if got := c.Argv(); !reflect.DeepEqual(got, want) {
		t.Errorf("Argv() = %v, want %v", got, want)
	}
}

func TestArgvDoesNotFilterInjected(t *testing.T) {
	// -c is banned in UserArgs but required in InjectedArgs. Argv must not touch
	// it — the deny-list applies only where ValidateUserArgs is called.
	c := Cmd{InjectedArgs: []string{"-c", "credential.helper=x"}, UserArgs: []string{"push"}}
	argv := c.Argv()
	if argv[0] != "-c" {
		t.Fatalf("injected -c was dropped: %v", argv)
	}
}

func TestBuildEnvIsAnAllowlist(t *testing.T) {
	// Set a variable that must never be forwarded.
	t.Setenv("GIT_CONFIG_GLOBAL", "/tmp/evil")
	t.Setenv("GIT_SSH_COMMAND", "sh -c id")
	t.Setenv("GIT_EXTERNAL_DIFF", "sh -c id")
	t.Setenv("GIT_PROXY_COMMAND", "sh -c id")

	env := BuildEnv(AuthSpec{Method: "askpass", Username: "x-access-token", Token: "secret"}, "/opt/wick/git-plugin")

	joined := strings.Join(env, "\n")
	for _, banned := range []string{"GIT_CONFIG_GLOBAL", "GIT_SSH_COMMAND", "GIT_EXTERNAL_DIFF", "GIT_PROXY_COMMAND"} {
		if strings.Contains(joined, banned) {
			t.Errorf("%s leaked into the child environment:\n%s", banned, joined)
		}
	}
}

func TestBuildEnvSetsRequiredVars(t *testing.T) {
	env := BuildEnv(AuthSpec{Method: "askpass", Username: "u", Token: "t"}, "/opt/wick/git-plugin")
	want := map[string]string{
		"GIT_TERMINAL_PROMPT": "0",
		"GIT_ASKPASS":         "/opt/wick/git-plugin",
		"WICK_GIT_USERNAME":   "u",
		"WICK_GIT_TOKEN":      "t",
	}
	got := envMap(env)
	for k, v := range want {
		if got[k] != v {
			t.Errorf("env[%s] = %q, want %q", k, got[k], v)
		}
	}
}

func TestBuildEnvOmitsAskpassWhenNoToken(t *testing.T) {
	env := envMap(BuildEnv(AuthSpec{Method: "askpass"}, "/opt/wick/git-plugin"))
	if _, ok := env["GIT_ASKPASS"]; ok {
		t.Error("GIT_ASKPASS set with no token — git would call the helper for nothing")
	}
	if env["GIT_TERMINAL_PROMPT"] != "0" {
		t.Error("GIT_TERMINAL_PROMPT must be 0 even without a token, so git never blocks on a prompt")
	}
}

func TestAuthInjectedArgs(t *testing.T) {
	t.Run("askpass injects nothing", func(t *testing.T) {
		if got := AuthInjectedArgs(AuthSpec{Method: "askpass", Token: "t"}); got != nil {
			t.Errorf("got %v, want nil — askpass works through env only", got)
		}
	})

	t.Run("credential helper keeps the token out of argv", func(t *testing.T) {
		got := AuthInjectedArgs(AuthSpec{Method: "credential_helper", Username: "u", Token: "secret-token"})
		joined := strings.Join(got, " ")
		if strings.Contains(joined, "secret-token") {
			t.Errorf("token leaked into argv: %s", joined)
		}
		if !strings.Contains(joined, envAskpassToken) {
			t.Errorf("helper must read the token from env, got: %s", joined)
		}
	})

	t.Run("extraheader carries the token in argv by design", func(t *testing.T) {
		got := AuthInjectedArgs(AuthSpec{Method: "extraheader", Username: "u", Token: "t"})
		if len(got) != 2 || !strings.Contains(got[1], "Authorization: Basic ") {
			t.Errorf("got %v, want an http.extraheader -c pair", got)
		}
	})

	t.Run("no token injects nothing", func(t *testing.T) {
		if got := AuthInjectedArgs(AuthSpec{Method: "extraheader"}); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})
}

func TestBasicAuthValue(t *testing.T) {
	// "u:p" base64-encoded.
	if got := basicAuthValue("u", "p"); got != "dTpw" {
		t.Errorf("basicAuthValue = %q, want %q", got, "dTpw")
	}
}

// envMap turns a KEY=VALUE slice into a map for assertions.
func envMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, e := range env {
		if i := strings.Index(e, "="); i > 0 {
			out[e[:i]] = e[i+1:]
		}
	}
	return out
}

func TestCapBytes(t *testing.T) {
	s, truncated := capBytes([]byte("hello"), 10)
	if s != "hello" || truncated {
		t.Errorf("capBytes under limit = (%q, %v), want (\"hello\", false)", s, truncated)
	}

	s, truncated = capBytes([]byte("hello world"), 5)
	if !truncated {
		t.Error("truncated = false, want true")
	}
	if s != "hello" {
		t.Errorf("capBytes = %q, want the first 5 bytes", s)
	}

	s, truncated = capBytes([]byte("abc"), 0)
	if s != "abc" || truncated {
		t.Errorf("max 0 means no limit, got (%q, %v)", s, truncated)
	}
}

func TestValidateRepoPath(t *testing.T) {
	t.Run("rejects a directory without .git", func(t *testing.T) {
		dir := t.TempDir()
		if err := ValidateRepoPath(dir); err == nil {
			t.Fatal("expected an error for a directory with no .git")
		}
	})

	t.Run("accepts a directory with .git", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := ValidateRepoPath(dir); err != nil {
			t.Fatalf("ValidateRepoPath = %v, want nil", err)
		}
	})

	t.Run("rejects empty", func(t *testing.T) {
		if err := ValidateRepoPath(""); err == nil {
			t.Fatal("expected an error for an empty path")
		}
	})

	t.Run("rejects a nonexistent path", func(t *testing.T) {
		if err := ValidateRepoPath(filepath.Join(t.TempDir(), "nope")); err == nil {
			t.Fatal("expected an error for a nonexistent path")
		}
	})

	t.Run("rejects the home directory itself", func(t *testing.T) {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skip("no home directory in this environment")
		}
		// The guard must fire even if $HOME happens to be a repo, so the error is
		// required regardless of whether a .git is present there.
		err = ValidateRepoPath(home)
		if err == nil {
			t.Fatalf("ValidateRepoPath(%q) = nil, want the home-directory guard to reject it", home)
		}
		if !strings.Contains(err.Error(), "home directory") {
			t.Errorf("error should name the home-directory guard, got: %v", err)
		}
	})
}

func TestRunCapturesOutput(t *testing.T) {
	if _, err := ResolveGit(); err != nil {
		t.Skip("git not installed")
	}
	dir := initTestRepo(t)

	res, err := Run(context.Background(),
		Cmd{RepoPath: dir, UserArgs: []string{"status", "--porcelain=v2"}},
		RunOpts{Timeout: 30 * time.Second, MaxOutput: 1 << 20})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.OK || res.ExitCode != 0 {
		t.Errorf("OK = %v, ExitCode = %d, stderr = %s", res.OK, res.ExitCode, res.Stderr)
	}
	if res.DurationMS < 0 {
		t.Errorf("DurationMS = %d, want >= 0", res.DurationMS)
	}
}

func TestRunReportsNonZeroExit(t *testing.T) {
	if _, err := ResolveGit(); err != nil {
		t.Skip("git not installed")
	}
	dir := initTestRepo(t)

	res, err := Run(context.Background(),
		Cmd{RepoPath: dir, UserArgs: []string{"rev-parse", "does-not-exist"}},
		RunOpts{Timeout: 30 * time.Second, MaxOutput: 1 << 20})
	// A non-zero git exit is a Result, not a Go error — the agent needs stderr.
	if err != nil {
		t.Fatalf("Run returned an error for a non-zero exit; want it in Result: %v", err)
	}
	if res.OK || res.ExitCode == 0 {
		t.Errorf("OK = %v, ExitCode = %d, want a failure recorded", res.OK, res.ExitCode)
	}
	if res.Stderr == "" {
		t.Error("Stderr empty, want git's message")
	}
}

func TestRunTruncatesOutput(t *testing.T) {
	if _, err := ResolveGit(); err != nil {
		t.Skip("git not installed")
	}
	dir := initTestRepo(t)

	res, err := Run(context.Background(),
		Cmd{RepoPath: dir, UserArgs: []string{"log", "--format=%H %s"}},
		RunOpts{Timeout: 30 * time.Second, MaxOutput: 5})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Truncated {
		t.Error("Truncated = false, want true with MaxOutput 5")
	}
	if len(res.Stdout) > 5 {
		t.Errorf("Stdout is %d bytes, want at most 5", len(res.Stdout))
	}
}

func TestRunMasksSecrets(t *testing.T) {
	if _, err := ResolveGit(); err != nil {
		t.Skip("git not installed")
	}
	dir := initTestRepo(t)

	res, err := Run(context.Background(),
		Cmd{RepoPath: dir, UserArgs: []string{"log", "--format=%s"}},
		RunOpts{Timeout: 30 * time.Second, MaxOutput: 1 << 20, Masks: []string{"initial"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(res.Stdout, "initial") {
		t.Errorf("masked value leaked into stdout: %q", res.Stdout)
	}
}

// With auth_method=extraheader the credential reaches argv base64-encoded. base64
// is encoding, not protection — it decodes in one step — so the caller passes the
// encoded form in Masks and mask must redact it from the recorded command.
func TestRunMasksBase64Credential(t *testing.T) {
	if _, err := ResolveGit(); err != nil {
		t.Skip("git not installed")
	}
	dir := initTestRepo(t)

	const token = "ghp_supersecrettoken"
	encoded := basicAuthValue("x-access-token", token)

	res, err := Run(context.Background(),
		Cmd{
			RepoPath:     dir,
			InjectedArgs: []string{"-c", "http.extraheader=Authorization: Basic " + encoded},
			UserArgs:     []string{"status", "--porcelain=v2"},
		},
		RunOpts{Timeout: 30 * time.Second, MaxOutput: 1 << 20, Masks: []string{token, encoded}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if strings.Contains(res.Command, encoded) {
		t.Errorf("base64 credential leaked into Result.Command: %q", res.Command)
	}
	if strings.Contains(res.Command, token) {
		t.Errorf("raw token leaked into Result.Command: %q", res.Command)
	}
	// The mask must not be defeated by decoding either.
	if dec, derr := base64.StdEncoding.DecodeString(encoded); derr == nil {
		if strings.Contains(res.Command, string(dec)) {
			t.Errorf("decoded credential present in Result.Command: %q", res.Command)
		}
	}
}

// A real token is never below the length floor mask uses to protect ordinary text.
func TestMaskLengthFloor(t *testing.T) {
	if got := mask("abc def", []string{"abc"}); got != "abc def" {
		t.Errorf("mask must skip values shorter than 4 chars, got %q", got)
	}
	const token = "ghp_supersecrettoken"
	if got := mask("url https://x:"+token+"@example.com", []string{token}); strings.Contains(got, token) {
		t.Errorf("a real token must be redacted, got %q", got)
	}
	if got := mask("abcd efgh", []string{"abcd"}); strings.Contains(got, "abcd") {
		t.Errorf("a 4-char value is at the floor and must be redacted, got %q", got)
	}
}

// Run must validate only UserArgs. Validating Argv() would reject the plugin's own
// "-c credential.helper=..." injection and silently break all authentication.
func TestRunAcceptsInjectedDashC(t *testing.T) {
	if _, err := ResolveGit(); err != nil {
		t.Skip("git not installed")
	}
	dir := initTestRepo(t)

	res, err := Run(context.Background(),
		Cmd{
			RepoPath:     dir,
			InjectedArgs: AuthInjectedArgs(AuthSpec{Method: "credential_helper", Username: "u", Token: "secret-token"}),
			UserArgs:     []string{"status", "--porcelain=v2"},
		},
		RunOpts{Timeout: 30 * time.Second, MaxOutput: 1 << 20, Masks: []string{"secret-token"}})
	if err != nil {
		t.Fatalf("Run rejected an injected -c: %v", err)
	}
	if !res.OK {
		t.Errorf("OK = false with an injected credential helper; exit=%d stderr=%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Command, "credential.helper") {
		t.Errorf("injected -c was dropped from the command: %q", res.Command)
	}
}

// Run must still reject banned flags arriving from the agent.
func TestRunRejectsBannedUserArgs(t *testing.T) {
	if _, err := ResolveGit(); err != nil {
		t.Skip("git not installed")
	}
	dir := initTestRepo(t)

	_, err := Run(context.Background(),
		Cmd{RepoPath: dir, UserArgs: []string{"-c", "core.pager=sh -c id", "log"}},
		RunOpts{Timeout: 30 * time.Second, MaxOutput: 1 << 20})
	if err == nil {
		t.Fatal("Run accepted a banned user argument, want an error")
	}
}

func TestRunTimeoutKillsProcess(t *testing.T) {
	if _, err := ResolveGit(); err != nil {
		t.Skip("git not installed")
	}
	dir := initTestRepo(t)

	start := time.Now()
	// ls-remote against an unroutable address hangs until the timeout fires.
	res, err := Run(context.Background(),
		Cmd{RepoPath: dir, Network: true,
			UserArgs: []string{"ls-remote", "https://10.255.255.1/org/repo.git"}},
		RunOpts{Timeout: 2 * time.Second, MaxOutput: 1 << 20})
	elapsed := time.Since(start)

	if err == nil && res.OK {
		t.Skip("the unroutable host answered; cannot assert a timeout here")
	}
	if elapsed > 20*time.Second {
		t.Errorf("took %v, want the 2s timeout to have killed it", elapsed)
	}
}

// initTestRepo creates a temp git repo with one commit. Identity is passed per
// command so the test never depends on the machine's git config.
func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitPath, err := ResolveGit()
	if err != nil {
		t.Skip("git not installed")
	}
	run := func(args ...string) {
		t.Helper()
		cmd := safeexec.Command(gitPath, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial")
	return dir
}
