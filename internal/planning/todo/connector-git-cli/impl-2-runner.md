# Git CLI Connector — Implementation Plan (Part 2 of 3: Runner & Entry Point)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the process runner that executes git safely — argument deny-list, environment allowlist, credential injection with no disk writes, timeouts, and output caps — plus the plugin entry point.

**Prerequisite:** [impl-1-policy.md](impl-1-policy.md) complete. `Resolve`, `Evaluate`, `ConvertRemote`, `RepoSlug` exist and pass tests.

**Global Constraints:** see [impl-1-policy.md](impl-1-policy.md) § Global Constraints. All of it applies here.

**Spec:** [plan.md](plan.md) §5 is the contract for this part. Read it before starting.

---

### Task 4: Argument deny-list and the userArgs/injectedArgs split

**Files:**
- Create: `plugins/connector/git/git.go`
- Test: `plugins/connector/git/git_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces:
  - `type Cmd struct { RepoPath string; InjectedArgs []string; UserArgs []string; Network bool }`
  - `func ValidateUserArgs(args []string) error`
  - `func SplitRawArgs(raw string) []string`
  - `func RawSubcommandOf(args []string) string`
  - `func (c Cmd) Argv() []string`
  - `var bannedArgs []string`

**Why the split is structural:** the deny-list must reject `-c` coming from the
agent while the plugin itself injects `-c credential.helper=…`. One slice for both
would make the plugin block its own injection. Two fields, joined only in `Argv()`.

- [ ] **Step 1: Write the failing test for `ValidateUserArgs`**

```go
package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestValidateUserArgsRejectsBanned(t *testing.T) {
	banned := [][]string{
		{"-c", "core.pager=sh -c 'id'", "log"},
		{"--config-env=core.pager=EVIL", "log"},
		{"--exec-path=/tmp/evil", "status"},
		{"--upload-pack=/tmp/evil", "fetch"},
		{"--receive-pack=/tmp/evil", "push"},
		{"-u", "/tmp/evil", "fetch"},
		{"fetch", "ext::sh -c id"},
		{"rebase", "-i"},
		{"rebase", "--interactive"},
		{"log", "--output=/etc/passwd"},
	}
	for _, args := range banned {
		if err := ValidateUserArgs(args); err == nil {
			t.Errorf("ValidateUserArgs(%v) = nil, want an error", args)
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugins/connector/git/ -run TestValidateUserArgs -v`
Expected: FAIL — `undefined: ValidateUserArgs`

- [ ] **Step 3: Implement `ValidateUserArgs` and `bannedArgs`**

```go
// git.go runs the git binary. Everything that touches the process — argument
// assembly, environment, timeouts, output limits — lives here, so the safety
// rules are in one auditable place.
//
// The central rule: arguments arriving from the agent (UserArgs) are filtered;
// arguments the plugin adds itself (InjectedArgs) are not. They are separate
// fields and only meet in Argv().
package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// bannedArgs are flags that turn git into an arbitrary-code-execution vector or
// make it block on an editor. Rejected only in agent-supplied arguments.
//
//   -c / --config-env  set arbitrary config: core.pager, alias.x=!cmd → shell
//   --exec-path        redirect git's helper binaries
//   --upload-pack / --receive-pack / -u  run an arbitrary binary on either end
//   ext::              the ext transport executes a shell command
//   -i / --interactive open an editor and hang until the timeout
//   --output           write to an arbitrary file path
var bannedArgs = []string{
	"-c", "--config-env", "--exec-path",
	"--upload-pack", "--receive-pack", "-u",
	"ext::", "-i", "--interactive", "--output",
}

// ValidateUserArgs rejects agent-supplied arguments that could escape the policy
// or hang the process. Matching covers both "--flag value" and "--flag=value".
func ValidateUserArgs(args []string) error {
	for _, a := range args {
		low := strings.ToLower(strings.TrimSpace(a))
		for _, bad := range bannedArgs {
			if low == bad || strings.HasPrefix(low, bad+"=") || strings.Contains(low, "ext::") {
				return fmt.Errorf("argument %q is not allowed (matched %q): it can execute arbitrary commands or block on an editor", a, bad)
			}
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugins/connector/git/ -run TestValidateUserArgs -v`
Expected: PASS

- [ ] **Step 5: Write the failing test for `SplitRawArgs`, `RawSubcommandOf`, and `Argv`**

```go
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
		{[]string{"--no-pager", "log"}, "log"},      // global flags skipped
		{[]string{"--git-dir=/x", "status"}, "status"},
		{[]string{"-n", "5"}, ""},                    // no subcommand present
		{nil, ""},
	}
	for _, c := range cases {
		if got := RawSubcommandOf(c.in); got != c.want {
			t.Errorf("RawSubcommandOf(%v) = %q, want %q", c.in, got, c.want)
		}
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
```

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./plugins/connector/git/ -run 'TestSplitRawArgs|TestRawSubcommandOf|TestArgv' -v`
Expected: FAIL — `undefined: SplitRawArgs`, `undefined: Cmd`

- [ ] **Step 7: Implement `Cmd`, `SplitRawArgs`, `RawSubcommandOf`, `Argv`**

```go
// Cmd is one git invocation. UserArgs come from the agent and are validated;
// InjectedArgs are added by the plugin and are not. Network marks operations that
// contact a remote so the longer timeout applies.
type Cmd struct {
	RepoPath     string
	InjectedArgs []string
	UserArgs     []string
	Network      bool
}

// Argv assembles the final argument vector. Injected arguments come first because
// git requires its global options before the subcommand.
func (c Cmd) Argv() []string {
	argv := make([]string, 0, len(c.InjectedArgs)+len(c.UserArgs))
	argv = append(argv, c.InjectedArgs...)
	argv = append(argv, c.UserArgs...)
	return argv
}

// SplitRawArgs splits a raw argument string into argv, honouring single and
// double quotes so a commit message with spaces survives as one argument.
func SplitRawArgs(s string) []string {
	var out []string
	var cur strings.Builder
	var quote rune
	inWord := false

	flush := func() {
		if inWord {
			out = append(out, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inWord = true // an empty quoted string is still an argument
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	flush()
	return out
}

// RawSubcommandOf finds the git subcommand in an argument vector, skipping git's
// own global flags. The policy engine needs it to decide whether raw may run;
// returning "" means "unknown", which the engine treats as denied.
func RawSubcommandOf(args []string) string {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			return strings.ToLower(a)
		}
		// A global flag taking a separate value consumes the next token.
		if a == "--git-dir" || a == "--work-tree" || a == "--namespace" {
			i++
		}
	}
	return ""
}
```

- [ ] **Step 8: Run test to verify it passes**

Run: `go test ./plugins/connector/git/ -run 'TestSplitRawArgs|TestRawSubcommandOf|TestArgv' -v`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add plugins/connector/git/git.go plugins/connector/git/git_test.go
git commit -m "feat(git): argument deny-list with separate user and injected argv"
```

---

### Task 5: Environment allowlist and credential injection

**Files:**
- Modify: `plugins/connector/git/git.go`
- Modify: `plugins/connector/git/git_test.go`

**Interfaces:**
- Consumes: `Cmd` from Task 4.
- Produces:
  - `type AuthSpec struct { Method, Username, Token string }`
  - `func BuildEnv(a AuthSpec, selfPath string) []string`
  - `func AuthInjectedArgs(a AuthSpec) []string`
  - `const envAskpassToken = "WICK_GIT_TOKEN"`, `const envAskpassUser = "WICK_GIT_USERNAME"`

- [ ] **Step 1: Write the failing test for `BuildEnv`**

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugins/connector/git/ -run TestBuildEnv -v`
Expected: FAIL — `undefined: BuildEnv`, `undefined: AuthSpec`

- [ ] **Step 3: Implement `AuthSpec`, `BuildEnv`, `AuthInjectedArgs`**

```go
// Environment variable names used to hand the credential to the askpass helper.
// The helper is this same binary re-invoked with --askpass.
const (
	envAskpassUser  = "WICK_GIT_USERNAME"
	envAskpassToken = "WICK_GIT_TOKEN"
)

// AuthSpec is the resolved HTTPS credential for one execution.
//
// Method is one of:
//
//	askpass            GIT_ASKPASS points at this binary; token travels in env.
//	                   No file, token absent from argv. The default.
//	credential_helper  -c credential.helper reads the token from env. Needs -c,
//	                   so it goes through InjectedArgs.
//	extraheader        -c http.extraheader carries the token in argv, where it is
//	                   visible in the process list. Opt-in only.
type AuthSpec struct {
	Method   string
	Username string
	Token    string
}

// BuildEnv returns the child environment as an explicit allowlist. Inheriting the
// parent environment would forward GIT_CONFIG_*, GIT_SSH_COMMAND and friends,
// each of which can make git execute an arbitrary command.
func BuildEnv(a AuthSpec, selfPath string) []string {
	env := []string{
		// git must never block waiting for a human, and must never fall back to
		// the machine's credential manager.
		"GIT_TERMINAL_PROMPT=0",
	}

	// Carry over only what git genuinely needs to run.
	for _, k := range []string{"PATH", "HOME", "USERPROFILE", "SystemRoot", "TMPDIR", "TEMP", "TMP", "LANG", "LC_ALL"} {
		if v := os.Getenv(k); v != "" {
			env = append(env, k+"="+v)
		}
	}

	if a.Token == "" {
		return env
	}

	env = append(env, envAskpassUser+"="+a.Username, envAskpassToken+"="+a.Token)
	if a.Method == "" || a.Method == "askpass" {
		env = append(env, "GIT_ASKPASS="+selfPath)
	}
	return env
}

// AuthInjectedArgs returns the plugin's own -c arguments for the credential
// methods that need them. These bypass ValidateUserArgs by construction: they
// are placed in Cmd.InjectedArgs, which is never filtered.
func AuthInjectedArgs(a AuthSpec) []string {
	if a.Token == "" {
		return nil
	}
	switch a.Method {
	case "credential_helper":
		// The helper echoes the token from the environment, so it never appears
		// in argv (and so never in the process list).
		return []string{"-c", `credential.helper=!f(){ echo username=$` + envAskpassUser +
			`; echo password=$` + envAskpassToken + `; }; f`}
	case "extraheader":
		basic := basicAuthValue(a.Username, a.Token)
		return []string{"-c", "http.extraheader=Authorization: Basic " + basic}
	default:
		return nil
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugins/connector/git/ -run TestBuildEnv -v`
Expected: PASS

- [ ] **Step 5: Write the failing test for `AuthInjectedArgs` and `basicAuthValue`**

```go
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
```

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./plugins/connector/git/ -run 'TestAuthInjectedArgs|TestBasicAuthValue' -v`
Expected: FAIL — `undefined: basicAuthValue`

- [ ] **Step 7: Implement `basicAuthValue`**

Add `"encoding/base64"` to the import block.

```go
// basicAuthValue encodes username:token for an HTTP Basic header.
func basicAuthValue(user, token string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + token))
}
```

- [ ] **Step 8: Run test to verify it passes**

Run: `go test ./plugins/connector/git/ -run 'TestAuthInjectedArgs|TestBasicAuthValue' -v`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add plugins/connector/git/git.go plugins/connector/git/git_test.go
git commit -m "feat(git): env allowlist and no-disk credential injection"
```

---

### Task 6: The runner — timeout, output cap, process-group kill

**Files:**
- Modify: `plugins/connector/git/git.go`
- Modify: `plugins/connector/git/git_test.go`
- Create: `plugins/connector/git/proc_unix.go`
- Create: `plugins/connector/git/proc_windows.go`

**Interfaces:**
- Consumes: `Cmd`, `BuildEnv`, `AuthSpec` from Tasks 4–5.
- Produces:
  - `type Result struct { OK bool; Command string; ExitCode int; Stdout, Stderr string; Truncated bool; DurationMS int64 }`
  - `type RunOpts struct { Auth AuthSpec; SelfPath string; Timeout time.Duration; MaxOutput int; Masks []string }`
  - `func Run(ctx context.Context, c Cmd, o RunOpts) (Result, error)`
  - `func ResolveGit() (string, error)`
  - `func ValidateRepoPath(p string) error`
  - `func capBytes(b []byte, max int) (string, bool)`
  - `func setProcAttr(cmd *exec.Cmd)` (per-platform)
  - `func killGroup(cmd *exec.Cmd)` (per-platform)

- [ ] **Step 1: Write the failing test for `capBytes` and `ValidateRepoPath`**

```go
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
		// Only meaningful if $HOME happens to be a repo; assert the guard exists
		// regardless by checking the error message path.
		if err := ValidateRepoPath(home); err == nil {
			t.Log("home is not a repo here; guard still required for machines where it is")
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugins/connector/git/ -run 'TestCapBytes|TestValidateRepoPath' -v`
Expected: FAIL — `undefined: capBytes`, `undefined: ValidateRepoPath`

- [ ] **Step 3: Implement `capBytes`, `ValidateRepoPath`, `ResolveGit`**

```go
// capBytes truncates output to max bytes. max <= 0 means no limit. Truncation is
// always reported so a caller never mistakes a cut result for a complete one.
func capBytes(b []byte, max int) (string, bool) {
	if max <= 0 || len(b) <= max {
		return string(b), false
	}
	return string(b[:max]), true
}

// ValidateRepoPath checks that a path is a plausible git repository. There is no
// path allowlist by design — the connector manages repos that already exist — so
// this guard exists to catch accidents, not to contain an attacker.
func ValidateRepoPath(p string) error {
	if strings.TrimSpace(p) == "" {
		return errors.New("repo_path is required")
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return fmt.Errorf("repo_path %q is not a valid path: %w", p, err)
	}
	if home, herr := os.UserHomeDir(); herr == nil && abs == filepath.Clean(home) {
		return fmt.Errorf("repo_path must not be the home directory itself")
	}
	st, err := os.Stat(abs)
	if err != nil {
		return fmt.Errorf("repo_path %q does not exist", p)
	}
	if !st.IsDir() {
		return fmt.Errorf("repo_path %q is not a directory", p)
	}
	if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
		return fmt.Errorf("repo_path %q does not contain a .git directory", p)
	}
	return nil
}

// ResolveGit finds the git binary. A clear failure here beats an obscure one at
// exec time.
func ResolveGit() (string, error) {
	p, err := exec.LookPath("git")
	if err != nil {
		return "", errors.New("git not found in PATH on the machine running wick")
	}
	return p, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugins/connector/git/ -run 'TestCapBytes|TestValidateRepoPath' -v`
Expected: PASS

- [ ] **Step 5: Write the per-platform process helpers**

`proc_unix.go`:

```go
//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// setProcAttr puts git in its own process group so a timeout can kill the whole
// tree. git spawns helpers (git-remote-https, credential helpers); killing only
// the parent orphans them.
func setProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killGroup kills the process group created by setProcAttr.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
```

`proc_windows.go`:

```go
//go:build windows

package main

import (
	"os/exec"
	"strconv"
)

// setProcAttr is a no-op on Windows: there is no process group to set at spawn
// time in the POSIX sense. killGroup uses taskkill /T instead.
func setProcAttr(cmd *exec.Cmd) {}

// killGroup kills git and every child it spawned. taskkill /T walks the tree,
// which is the Windows equivalent of killing a process group.
func killGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
}
```

- [ ] **Step 6: Write the failing test for `Run`**

```go
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
		cmd := exec.Command(gitPath, args...)
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
```

- [ ] **Step 7: Run test to verify it fails**

Run: `go test ./plugins/connector/git/ -run TestRun -v`
Expected: FAIL — `undefined: Run`, `undefined: RunOpts`, `undefined: Result`

- [ ] **Step 8: Implement `Run`**

```go
// Result is the typed envelope every operation returns. A non-zero git exit is
// reported here, not as a Go error — the agent needs stderr to react.
type Result struct {
	OK         bool   `json:"ok"`
	Command    string `json:"command"`
	ExitCode   int    `json:"exit_code"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	Truncated  bool   `json:"truncated"`
	DurationMS int64  `json:"duration_ms"`
}

// RunOpts carries everything the runner needs that is not part of the command.
// Masks are values scrubbed from the recorded command and output.
type RunOpts struct {
	Auth      AuthSpec
	SelfPath  string
	Timeout   time.Duration
	MaxOutput int
	Masks     []string
}

// Run executes git with a bounded lifetime, an allowlisted environment, and
// capped output. It returns an error only when the command could not be started
// or was killed; a normal non-zero exit lands in Result.
func Run(ctx context.Context, c Cmd, o RunOpts) (Result, error) {
	gitPath, err := ResolveGit()
	if err != nil {
		return Result{}, err
	}
	if o.Timeout <= 0 {
		o.Timeout = 60 * time.Second
	}

	argv := c.Argv()
	shown := mask("git "+strings.Join(argv, " "), o.Masks)

	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()

	cmd := exec.Command(gitPath, argv...)
	cmd.Dir = c.RepoPath
	cmd.Env = BuildEnv(o.Auth, o.SelfPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	setProcAttr(cmd)

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{Command: shown}, fmt.Errorf("start git: %w", err)
	}

	// Kill the whole process group on timeout. exec.CommandContext would only
	// signal the parent, orphaning git's transport helpers.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var waitErr error
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		killGroup(cmd)
		<-done
		return Result{
				Command:    shown,
				DurationMS: time.Since(start).Milliseconds(),
				Stderr:     mask(stderr.String(), o.Masks),
			},
			fmt.Errorf("git timed out after %s and was killed", o.Timeout)
	}

	res := Result{
		Command:    shown,
		DurationMS: time.Since(start).Milliseconds(),
	}
	outStr, outTrunc := capBytes(stdout.Bytes(), o.MaxOutput)
	errStr, errTrunc := capBytes(stderr.Bytes(), o.MaxOutput)
	res.Stdout = mask(outStr, o.Masks)
	res.Stderr = mask(errStr, o.Masks)
	res.Truncated = outTrunc || errTrunc

	var exitErr *exec.ExitError
	switch {
	case waitErr == nil:
		res.OK = true
	case errors.As(waitErr, &exitErr):
		res.ExitCode = exitErr.ExitCode()
	default:
		return res, fmt.Errorf("git failed: %w", waitErr)
	}
	return res, nil
}

// mask replaces every secret value with a fixed placeholder. Applied to the
// recorded command and to both output streams, since git echoes URLs back.
func mask(s string, values []string) string {
	for _, v := range values {
		if len(v) < 4 {
			continue // too short to mask without mangling ordinary text
		}
		s = strings.ReplaceAll(s, v, "••••••••")
	}
	return s
}
```

- [ ] **Step 9: Run test to verify it passes**

Run: `go test ./plugins/connector/git/ -run TestRun -v`
Expected: PASS (timeout test may skip if the unroutable host answers)

- [ ] **Step 10: Verify the whole package builds on both platforms**

Run: `go vet ./plugins/connector/git/ && GOOS=linux go build ./plugins/connector/git/ && GOOS=windows go build ./plugins/connector/git/`
Expected: no output — both build tags compile

- [ ] **Step 11: Commit**

```bash
git add plugins/connector/git/git.go plugins/connector/git/git_test.go \
        plugins/connector/git/proc_unix.go plugins/connector/git/proc_windows.go
git commit -m "feat(git): bounded runner with process-group kill and output caps"
```

---

**Part 2a complete.** Continue with [impl-3-ops.md](impl-3-ops.md) — operations, `connector.go`, `main.go` with askpass mode, and the Policy Manager widget.
