# Git CLI Connector — Implementation Plan (Part 3 of 3: Operations, Entry Point, Policy Manager)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Wire the policy engine and runner into connector operations, ship the plugin entry point with its askpass mode, and build the Policy Manager widget that makes the rules editable and testable from the manager UI.

**Prerequisite:** [impl-1-policy.md](impl-1-policy.md) and [impl-2-runner.md](impl-2-runner.md) complete.

**Global Constraints:** see [impl-1-policy.md](impl-1-policy.md) § Global Constraints. All of it applies here.

**Spec:** [plan.md](plan.md) §4 (operations), §6 (widget).

**API reference — verified signatures, use exactly these:**

```go
func Op[I any](key, name, description string, input I, exec ExecuteFunc, docs wickdocs.Docs) Operation
func OpDestructive[I any](key, name, description string, input I, exec ExecuteFunc, docs wickdocs.Docs) Operation
func OpConfigOnly[I any](key, name, description string, input I, exec ExecuteFunc, docs wickdocs.Docs) Operation
func Cat(title, description string, ops ...Operation) Category
type ExecuteFunc func(c *Ctx) (any, error)
```

`Ctx` accessors: `c.Cfg(key)`, `c.CfgInt(key)`, `c.CfgBool(key)`, `c.Input(key)`, `c.InputInt(key)`, `c.InputBool(key)`, `c.Context()`, `c.Mask(data, values)`.

---

### Task 7: Config struct and execution context assembly

**Files:**
- Create: `plugins/connector/git/connector.go`
- Create: `plugins/connector/git/service.go`
- Test: `plugins/connector/git/service_test.go`

**Interfaces:**
- Consumes: `GlobalPolicy`, `RepoRule`, `Resolve`, `ParseRepoRules`, `ParseKVList`, `ParseHostMap`, `AuthSpec`, `RunOpts` from Tasks 1–6.
- Produces:
  - `type Config struct { … }` (full field set below)
  - `func loadGlobal(c *connector.Ctx) GlobalPolicy`
  - `func loadAuth(c *connector.Ctx) AuthSpec`
  - `func runOpts(c *connector.Ctx, network bool) RunOpts`
  - `func policyFor(c *connector.Ctx, repoPath, repoSlug string) EffectivePolicy`
  - `func parseRawRules(s string) map[string]string`
  - `const Key = "git"`

- [ ] **Step 1: Write the failing test for `parseRawRules`**

```go
package main

import "testing"

func TestParseRawRules(t *testing.T) {
	in := `[{"subcommand":"bisect","mode":"allow"},{"subcommand":"Push","mode":"DENY"},{"subcommand":"","mode":"allow"}]`
	got := parseRawRules(in)

	if got["bisect"] != "allow" {
		t.Errorf("bisect = %q, want allow", got["bisect"])
	}
	// Keys and modes are normalised to lowercase so config casing cannot create
	// a rule that silently never matches.
	if got["push"] != "deny" {
		t.Errorf("push = %q, want deny (normalised from \"Push\"/\"DENY\")", got["push"])
	}
	if _, ok := got[""]; ok {
		t.Error("an empty subcommand must not become a rule")
	}
}

func TestParseRawRulesEmpty(t *testing.T) {
	if got := parseRawRules(""); len(got) != 0 {
		t.Errorf("parseRawRules(\"\") = %v, want empty", got)
	}
	if got := parseRawRules("not json"); len(got) != 0 {
		t.Errorf("malformed input must yield an empty map, got %v", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugins/connector/git/ -run TestParseRawRules -v`
Expected: FAIL — `undefined: parseRawRules`

- [ ] **Step 3: Write `service.go` with `parseRawRules` and the loaders**

```go
// service.go bridges connector config into the pure types from policy.go and
// git.go. It is the only place that reads c.Cfg — everything downstream takes
// typed values, which keeps the policy engine and runner testable without a Ctx.
package main

import (
	"strings"
	"time"

	"github.com/yogasw/wick/pkg/connector"
)

// parseRawRules turns the raw_rules kvlist into subcommand → mode. Keys and modes
// are lowercased so a row typed as "Push"/"DENY" still matches.
func parseRawRules(s string) map[string]string {
	rows, err := ParseKVList(s)
	if err != nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		sub := strings.ToLower(strings.TrimSpace(r["subcommand"]))
		mode := strings.ToLower(strings.TrimSpace(r["mode"]))
		if sub == "" || (mode != "allow" && mode != "deny") {
			continue
		}
		out[sub] = mode
	}
	return out
}

// loadGlobal reads layer 1 of the policy from config.
func loadGlobal(c *connector.Ctx) GlobalPolicy {
	protected := make([]string, 0, 4)
	if rows, err := ParseKVList(c.Cfg("protected_branches")); err == nil {
		for _, r := range rows {
			if b := strings.TrimSpace(r["branch"]); b != "" {
				protected = append(protected, b)
			}
		}
	}
	return GlobalPolicy{
		BranchPattern:  strings.TrimSpace(c.Cfg("branch_name_pattern")),
		Protected:      protected,
		AllowForcePush: c.CfgBool("allow_force_push"),
		RawEnabled:     c.CfgBool("raw_enabled"),
		RawRules:       parseRawRules(c.Cfg("raw_rules")),
	}
}

// policyFor compiles the effective policy for one repo. Invalid repo_policies
// JSON is recorded as PolicyErr rather than ignored, so mutations fail closed.
func policyFor(c *connector.Ctx, repoPath, repoSlug string) EffectivePolicy {
	global := loadGlobal(c)
	rules, err := ParseRepoRules(c.Cfg("repo_policies"))
	if err != nil {
		p := Resolve(global, nil, repoPath, repoSlug)
		p.PolicyErr = "repo_policies is not valid JSON: " + err.Error()
		return p
	}
	return Resolve(global, rules, repoPath, repoSlug)
}

// loadAuth reads the HTTPS credential. An empty token is valid — the repo may be
// public, or the operation may not touch the network.
func loadAuth(c *connector.Ctx) AuthSpec {
	method := strings.TrimSpace(c.Cfg("auth_method"))
	if method == "" {
		method = "askpass"
	}
	return AuthSpec{
		Method:   method,
		Username: strings.TrimSpace(c.Cfg("username")),
		Token:    c.Cfg("token"),
	}
}

// runOpts assembles runner options, applying the network timeout for operations
// that contact a remote.
func runOpts(c *connector.Ctx, network bool) RunOpts {
	timeout := c.CfgInt("timeout_seconds")
	if network {
		if nt := c.CfgInt("network_timeout_seconds"); nt > 0 {
			timeout = nt
		} else {
			timeout = 180
		}
	}
	if timeout <= 0 {
		timeout = 60
	}
	maxOut := c.CfgInt("max_output_bytes")
	if maxOut <= 0 {
		maxOut = 262144
	}
	auth := loadAuth(c)

	masks := make([]string, 0, 1)
	if auth.Token != "" {
		masks = append(masks, auth.Token)
	}
	return RunOpts{
		Auth:      auth,
		SelfPath:  selfPath(),
		Timeout:   time.Duration(timeout) * time.Second,
		MaxOutput: maxOut,
		Masks:     masks,
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugins/connector/git/ -run TestParseRawRules -v`
Expected: PASS

- [ ] **Step 5: Write `connector.go` with `Config` and `Meta`**

```go
// Command git is the git CLI connector shipped as an external wick plugin.
//
// It wraps the local git binary rather than a hosting API, so GitHub, Bitbucket,
// GitLab and self-hosted servers all work through the same operations. A policy
// engine gates every mutation: branch naming rules, protected branches, force
// push, and an allow-list for the raw escape hatch.
//
// Credentials never touch disk. HTTPS tokens reach git through an askpass helper
// (this same binary, re-invoked with --askpass) and the plugin never rewrites
// .git/config.
//
// File layout:
//
//   - connector.go — Module, Meta, Config, Input structs, Operations, handlers
//   - service.go   — config → typed policy/auth/runner values
//   - policy.go    — policy resolution and evaluation (pure)
//   - remote.go    — remote URL parsing, credential stripping, SSH→HTTPS
//   - git.go       — process runner: argv, env, timeout, output caps
//   - policyui.go  — Policy Manager config widget
package main

import (
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/plugins/tags"
)

const Key = "git"

// Config is the per-instance setup: one identity, one credential, one policy set.
type Config struct {
	AuthorName  string `wick:"group=Identity;desc=Name used for commits made through this connector. Example: Deploy Bot"`
	AuthorEmail string `wick:"group=Identity;desc=Email used for commits. Example: bot@example.com"`

	Username   string `wick:"group=Authentication;desc=HTTPS username. For a GitHub personal access token use x-access-token."`
	Token      string `wick:"secret;group=Authentication;desc=Personal access token or app password. Passed to git through an askpass helper and never written to disk."`
	AuthMethod string `wick:"dropdown=askpass|credential_helper|extraheader;default=askpass;group=Authentication;desc=How the token reaches git. askpass keeps it out of the process list and is the safest. extraheader makes the token visible to anyone who can list processes."`

	ConvertSSHRemoteToHTTPS bool   `wick:"bool;default=true;group=Remote;desc=Rewrite SSH remotes to HTTPS for network operations. The repository's .git/config is never modified."`
	RemoteHostMap           string `wick:"kvlist=ssh_host|https_host;group=Remote;desc=Map SSH hosts to HTTPS hosts for self-hosted servers. Leave empty for GitHub, GitLab and Bitbucket."`

	BranchNamePattern string `wick:"group=Branch Policy;desc=Regular expression a new branch name must match. Example: ^(fix|feat|chore)/[a-z0-9._-]+$"`
	ProtectedBranches string `wick:"kvlist=branch;group=Branch Policy;desc=Protected branches. Direct pushes and commits are blocked. Globs allowed, for example release/*"`
	AllowForcePush    bool   `wick:"bool;group=Branch Policy;desc=Allow --force and --force-with-lease. Off by default."`

	RepoPolicies  string `wick:"hidden;desc=Per-repo policy rows, managed by the Policy Rules widget."`
	PolicyManager string `wick:"html=policy_manager;group=Policy Rules|Per-repo overrides and a simulator to test them before relying on them.;desc=Edit and test per-repo policy rules."`

	RawEnabled bool   `wick:"bool;group=Raw Operation;desc=Enable the raw operation, which runs an arbitrary git subcommand. Off by default."`
	RawRules   string `wick:"kvlist=subcommand|mode;group=Raw Operation;desc=Per-subcommand rules for raw. mode is allow or deny. A subcommand that is not listed is denied."`

	AllowHooks            bool `wick:"bool;group=Runtime;desc=Let repository hooks in .git/hooks run. Off by default, because a hook is arbitrary code from the repository."`
	TimeoutSeconds        int  `wick:"default=60;group=Runtime;desc=Timeout in seconds for operations that do not touch the network."`
	NetworkTimeoutSeconds int  `wick:"default=180;group=Runtime;desc=Timeout in seconds for push, pull, fetch, clone and ls-remote."`
	MaxOutputBytes        int  `wick:"default=262144;group=Runtime;desc=Maximum bytes of output returned. Larger output is truncated and flagged."`
}

// Meta identifies the connector. Key must equal the folder name.
func Meta() connector.Meta {
	return connector.Meta{
		Key:         Key,
		Name:        "Git CLI",
		Description: "Run git against local repositories with policy guards on branch names, protected branches and force pushes. Wraps the git binary, so it works with any host.",
		Icon:        "🌿",
	}
}

// Module assembles the definition served by main.go.
func Module() connector.Module {
	m := Meta()
	m.DefaultTags = []entity.DefaultTag{tags.Connector, tags.Development}
	return connector.Module{
		Meta:       m,
		Configs:    entity.StructToConfigs(Config{}),
		Operations: Operations(),
	}
}
```

- [ ] **Step 6: Write the failing test for config key derivation**

```go
func TestConfigKeysMatchWhatServiceReads(t *testing.T) {
	// entity.StructToConfigs derives snake_case keys. service.go reads those keys
	// by hand, so a rename on either side must fail loudly here.
	configs := entity.StructToConfigs(Config{})
	have := make(map[string]bool, len(configs))
	for _, cfg := range configs {
		have[cfg.Key] = true
	}
	for _, key := range []string{
		"author_name", "author_email",
		"username", "token", "auth_method",
		"convert_ssh_remote_to_https", "remote_host_map",
		"branch_name_pattern", "protected_branches", "allow_force_push",
		"repo_policies", "raw_enabled", "raw_rules",
		"allow_hooks", "timeout_seconds", "network_timeout_seconds", "max_output_bytes",
	} {
		if !have[key] {
			t.Errorf("config key %q is missing; service.go reads it via c.Cfg", key)
		}
	}
}
```

Add `"github.com/yogasw/wick/pkg/entity"` to the test file's imports.

- [ ] **Step 7: Run test to verify it passes**

Run: `go test ./plugins/connector/git/ -run TestConfigKeys -v`
Expected: PASS — if it fails, a tag or a `c.Cfg` call is misspelled

- [ ] **Step 8: Commit**

```bash
git add plugins/connector/git/connector.go plugins/connector/git/service.go \
        plugins/connector/git/service_test.go
git commit -m "feat(git): config schema and policy/auth loaders"
```

---

### Task 8: Read operations

**Files:**
- Modify: `plugins/connector/git/connector.go`
- Modify: `plugins/connector/git/service.go`
- Modify: `plugins/connector/git/service_test.go`

**Interfaces:**
- Consumes: everything from Task 7.
- Produces:
  - `type StatusInput`, `LogInput`, `DiffInput`, `BranchListInput`, `ShowInput`, `RemoteListInput`, `LsRemoteInput`
  - `func execute(c *connector.Ctx, op string, req Request, build func(EffectivePolicy) ([]string, error), network bool) (any, error)`
  - `func currentBranch(c *connector.Ctx, repoPath string) string`
  - `func remoteURL(c *connector.Ctx, repoPath, remote string) string`
  - `func Operations() []connector.Category` (read ops only in this task)

- [ ] **Step 1: Write the failing test for the response envelope**

```go
func TestEnvelopeShape(t *testing.T) {
	env := envelope(Result{OK: true, Command: "git status", ExitCode: 0, Stdout: "clean"},
		Verdict{Allow: true, MatchedRule: "global"}, nil)

	m, ok := env.(map[string]any)
	if !ok {
		t.Fatalf("envelope returned %T, want map[string]any", env)
	}
	for _, key := range []string{"ok", "command", "exit_code", "stdout", "stderr", "truncated", "duration_ms", "policy"} {
		if _, present := m[key]; !present {
			t.Errorf("envelope is missing key %q", key)
		}
	}
	pol, ok := m["policy"].(map[string]any)
	if !ok {
		t.Fatalf("policy is %T, want map[string]any", m["policy"])
	}
	if pol["verdict"] != "allow" || pol["matched_rule"] != "global" {
		t.Errorf("policy block = %v, want verdict allow and matched_rule global", pol)
	}
	if _, present := m["remote"]; present {
		t.Error("remote must be absent when no RemoteInfo was supplied")
	}
}

func TestEnvelopeIncludesRemoteWhenPresent(t *testing.T) {
	info := &RemoteInfo{Original: "git@github.com:org/repo.git",
		Effective: "https://github.com/org/repo.git", Converted: true}
	m := envelope(Result{OK: true}, Verdict{Allow: true, MatchedRule: "global"}, info).(map[string]any)

	rem, ok := m["remote"].(map[string]any)
	if !ok {
		t.Fatalf("remote is %T, want map[string]any", m["remote"])
	}
	if rem["converted"] != true || rem["effective"] != "https://github.com/org/repo.git" {
		t.Errorf("remote block = %v, want the converted URL reported", rem)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugins/connector/git/ -run TestEnvelope -v`
Expected: FAIL — `undefined: envelope`

- [ ] **Step 3: Implement `envelope` and `execute` in `service.go`**

```go
// envelope is the single response shape every operation returns. Reporting the
// policy verdict on success as well as failure means "why did this run" is always
// answerable, and reporting the effective remote makes a push to an unexpected
// host visible instead of mysterious.
func envelope(res Result, v Verdict, info *RemoteInfo) any {
	out := map[string]any{
		"ok":          res.OK,
		"command":     res.Command,
		"exit_code":   res.ExitCode,
		"stdout":      res.Stdout,
		"stderr":      res.Stderr,
		"truncated":   res.Truncated,
		"duration_ms": res.DurationMS,
		"policy": map[string]any{
			"evaluated":    true,
			"verdict":      verdictWord(v.Allow),
			"matched_rule": v.MatchedRule,
		},
	}
	if v.Reason != "" {
		out["policy"].(map[string]any)["reason"] = v.Reason
	}
	if info != nil {
		out["remote"] = map[string]any{
			"original":  info.Original,
			"effective": info.Effective,
			"converted": info.Converted,
		}
	}
	return out
}

func verdictWord(allow bool) string {
	if allow {
		return "allow"
	}
	return "deny"
}

// deniedEnvelope is returned when the policy blocks an operation. No process is
// spawned, so there is no Result to report.
func deniedEnvelope(v Verdict, command string) any {
	return envelope(Result{OK: false, Command: command, ExitCode: -1}, v, nil)
}

// execute is the shared path for every operation: validate the repo, compile the
// policy, evaluate, then run. build assembles the git arguments and runs after
// the policy passes, so a denied operation never even builds a command line.
func execute(c *connector.Ctx, op string, repoPath string, req Request,
	build func(EffectivePolicy) ([]string, error), network bool) (any, error) {

	if err := ValidateRepoPath(repoPath); err != nil {
		return nil, err
	}

	slug := RepoSlug(remoteURL(c, repoPath, firstNonEmpty(req.Remote, "origin")))
	pol := policyFor(c, repoPath, slug)
	req.Op = op

	v := pol.Evaluate(req)
	if !v.Allow {
		return deniedEnvelope(v, "git "+op), nil
	}

	userArgs, err := build(pol)
	if err != nil {
		return nil, err
	}
	if err := ValidateUserArgs(userArgs); err != nil {
		return nil, err
	}

	o := runOpts(c, network)
	cmd := Cmd{
		RepoPath:     repoPath,
		InjectedArgs: injectedArgs(c, o.Auth, op),
		UserArgs:     userArgs,
		Network:      network,
	}
	res, err := Run(c.Context(), cmd, o)
	if err != nil {
		return nil, err
	}
	return envelope(res, v, nil), nil
}

// injectedArgs are the plugin's own git options. They are never passed through
// ValidateUserArgs — that deny-list exists to filter agent input, and blocking
// our own injections would break credential handling and hook suppression.
func injectedArgs(c *connector.Ctx, auth AuthSpec, op string) []string {
	args := AuthInjectedArgs(auth)

	// Repository hooks are arbitrary code from the repository. Suppress them for
	// the operations that would run one, unless the admin opted in.
	if !c.CfgBool("allow_hooks") && hookRunningOps[op] {
		args = append(args, "-c", "core.hooksPath="+emptyHooksDir())
	}
	if name := strings.TrimSpace(c.Cfg("author_name")); name != "" {
		args = append(args, "-c", "user.name="+name)
	}
	if email := strings.TrimSpace(c.Cfg("author_email")); email != "" {
		args = append(args, "-c", "user.email="+email)
	}
	return args
}

// hookRunningOps are the operations git would run a hook for.
var hookRunningOps = map[string]bool{
	"commit": true, "merge": true, "push": true, "checkout": true, "rebase": true,
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
```

- [ ] **Step 4: Add `emptyHooksDir`, `selfPath`, `currentBranch`, `remoteURL` to `service.go`**

```go
// emptyHooksDir returns a directory guaranteed to contain no hooks. It is created
// once under the OS temp dir; an empty directory is not a secret and holds no
// state, so leaving it behind is harmless.
func emptyHooksDir() string {
	dir := filepath.Join(os.TempDir(), "wick-git-nohooks")
	_ = os.MkdirAll(dir, 0o700)
	return dir
}

// selfPath returns this binary's path, used as the GIT_ASKPASS helper.
func selfPath() string {
	if p, err := os.Executable(); err == nil {
		return p
	}
	return os.Args[0]
}

// currentBranch reads the checked-out branch. A detached HEAD yields "", which
// the policy treats as an unprotected, non-matching branch — mutations on a
// detached HEAD are still gated by the protected-branch check on the target.
func currentBranch(c *connector.Ctx, repoPath string) string {
	res, err := Run(c.Context(),
		Cmd{RepoPath: repoPath, UserArgs: []string{"rev-parse", "--abbrev-ref", "HEAD"}},
		runOpts(c, false))
	if err != nil || !res.OK {
		return ""
	}
	branch := strings.TrimSpace(res.Stdout)
	if branch == "HEAD" {
		return "" // detached
	}
	return branch
}

// remoteURL reads a remote's configured URL. Failure yields "", which makes the
// repo slug empty so only path-based policy rules can match — a safe default.
func remoteURL(c *connector.Ctx, repoPath, remote string) string {
	if remote == "" {
		remote = "origin"
	}
	res, err := Run(c.Context(),
		Cmd{RepoPath: repoPath, UserArgs: []string{"remote", "get-url", remote}},
		runOpts(c, false))
	if err != nil || !res.OK {
		return ""
	}
	return strings.TrimSpace(res.Stdout)
}
```

Add `"os"` and `"path/filepath"` to `service.go` imports.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./plugins/connector/git/ -run TestEnvelope -v`
Expected: PASS

- [ ] **Step 6: Add the read Input structs and handlers to `connector.go`**

```go
// StatusInput reports the working tree state.
type StatusInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository. Must contain a .git directory. Example: d:/code/work/api"`
}

// LogInput reads commit history. Limit keeps the response small; raise it only
// when a wider window is genuinely needed.
type LogInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Ref      string `wick:"desc=Branch, tag or commit to read. Default: the checked-out branch."`
	Limit    int    `wick:"default=20;desc=Maximum commits to return. Default 20."`
	Path     string `wick:"desc=Limit history to this file or directory, relative to the repository root."`
	Since    string `wick:"desc=Only commits after this date. Accepts anything git understands, for example 2026-01-01 or 2 weeks ago."`
}

// DiffInput compares two refs. StatOnly returns just the file summary, which is
// usually enough and far smaller than a full patch.
type DiffInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	RefA     string `wick:"desc=Left side of the comparison. Default: HEAD."`
	RefB     string `wick:"desc=Right side. Leave empty to diff the working tree against RefA."`
	Path     string `wick:"desc=Limit the diff to this file or directory."`
	StatOnly bool   `wick:"desc=Return only the changed-file summary instead of the full patch."`
	MaxLines int    `wick:"default=500;desc=Maximum patch lines to return. Default 500."`
}

// BranchListInput lists branches.
type BranchListInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Remote   bool   `wick:"desc=List remote-tracking branches instead of local ones."`
	Pattern  string `wick:"desc=Only branches matching this glob. Example: fix/*"`
}

// ShowInput reads one commit.
type ShowInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Ref      string `wick:"required;desc=Commit, tag or branch to show. Example: HEAD or a1b2c3d"`
}

// RemoteListInput lists remotes with the URL each operation would actually use.
type RemoteListInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
}

// LsRemoteInput probes a remote without changing anything — the cheapest way to
// verify that credentials and the remote URL both work.
type LsRemoteInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Remote   string `wick:"default=origin;desc=Remote name. Default: origin."`
}

func doStatus(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	return execute(c, "status", repo, Request{}, func(EffectivePolicy) ([]string, error) {
		return []string{"status", "--porcelain=v2", "--branch"}, nil
	}, false)
}

func doLog(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	return execute(c, "log", repo, Request{}, func(EffectivePolicy) ([]string, error) {
		limit := c.InputInt("limit")
		if limit <= 0 {
			limit = 20
		}
		args := []string{"log", "--format=%H%x09%an%x09%aI%x09%s", "-n", strconv.Itoa(limit)}
		if since := strings.TrimSpace(c.Input("since")); since != "" {
			args = append(args, "--since="+since)
		}
		if ref := strings.TrimSpace(c.Input("ref")); ref != "" {
			args = append(args, ref)
		}
		if p := strings.TrimSpace(c.Input("path")); p != "" {
			args = append(args, "--", p)
		}
		return args, nil
	}, false)
}

func doDiff(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	return execute(c, "diff", repo, Request{}, func(EffectivePolicy) ([]string, error) {
		args := []string{"diff"}
		if c.InputBool("stat_only") {
			args = append(args, "--stat")
		} else {
			maxLines := c.InputInt("max_lines")
			if maxLines <= 0 {
				maxLines = 500
			}
			args = append(args, "--unified=3")
		}
		refA := firstNonEmpty(strings.TrimSpace(c.Input("ref_a")), "HEAD")
		args = append(args, refA)
		if refB := strings.TrimSpace(c.Input("ref_b")); refB != "" {
			args = append(args, refB)
		}
		if p := strings.TrimSpace(c.Input("path")); p != "" {
			args = append(args, "--", p)
		}
		return args, nil
	}, false)
}

func doBranchList(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	return execute(c, "branch_list", repo, Request{}, func(EffectivePolicy) ([]string, error) {
		args := []string{"branch", "--format=%(refname:short)%09%(objectname:short)%09%(committerdate:iso8601)"}
		if c.InputBool("remote") {
			args = append(args, "--remotes")
		}
		if pat := strings.TrimSpace(c.Input("pattern")); pat != "" {
			args = append(args, "--list", pat)
		}
		return args, nil
	}, false)
}

func doShow(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	return execute(c, "show", repo, Request{}, func(EffectivePolicy) ([]string, error) {
		ref := strings.TrimSpace(c.Input("ref"))
		if ref == "" {
			return nil, errors.New("ref is required")
		}
		return []string{"show", "--stat", ref}, nil
	}, false)
}

func doRemoteList(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	if err := ValidateRepoPath(repo); err != nil {
		return nil, err
	}
	res, err := Run(c.Context(),
		Cmd{RepoPath: repo, UserArgs: []string{"remote", "-v"}}, runOpts(c, false))
	if err != nil {
		return nil, err
	}

	// Report the URL each network operation would actually use, not just what is
	// configured — that difference is exactly what surprises people.
	hostMap := ParseHostMap(c.Cfg("remote_host_map"))
	convert := c.CfgBool("convert_ssh_remote_to_https")
	remotes := make([]map[string]any, 0, 4)
	for _, name := range remoteNames(res.Stdout) {
		raw := remoteURL(c, repo, name)
		entry := map[string]any{"name": name, "configured": StripCredentials(raw)}
		if info, cerr := ConvertRemote(raw, hostMap, convert); cerr == nil {
			entry["effective"] = info.Effective
			entry["converted"] = info.Converted
		} else {
			entry["error"] = cerr.Error()
		}
		remotes = append(remotes, entry)
	}
	return map[string]any{"ok": true, "remotes": remotes}, nil
}

func doLsRemote(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	remote := firstNonEmpty(strings.TrimSpace(c.Input("remote")), "origin")
	if err := ValidateRepoPath(repo); err != nil {
		return nil, err
	}
	info, err := ConvertRemote(remoteURL(c, repo, remote),
		ParseHostMap(c.Cfg("remote_host_map")), c.CfgBool("convert_ssh_remote_to_https"))
	if err != nil {
		return nil, err
	}
	o := runOpts(c, true)
	res, err := Run(c.Context(), Cmd{
		RepoPath:     repo,
		InjectedArgs: injectedArgs(c, o.Auth, "ls_remote"),
		UserArgs:     []string{"ls-remote", "--heads", info.Effective},
		Network:      true,
	}, o)
	if err != nil {
		return nil, err
	}
	return envelope(res, Verdict{Allow: true, MatchedRule: "n/a (read-only)"}, &info), nil
}

// remoteNames extracts unique remote names from `git remote -v` output.
func remoteNames(out string) []string {
	seen := make(map[string]bool)
	var names []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || seen[fields[0]] {
			continue
		}
		seen[fields[0]] = true
		names = append(names, fields[0])
	}
	return names
}
```

Add `"errors"`, `"strconv"`, `"strings"` to `connector.go` imports.

- [ ] **Step 7: Add `Operations()` with the read category**

```go
// Operations returns the connector's operation tree. Read operations are safe by
// construction; mutating and destructive ones are added in later categories.
func Operations() []connector.Category {
	return []connector.Category{
		connector.Cat("Read", "Inspect a repository without changing it.",
			connector.Op("status", "Status",
				"Report the working tree state of the repository at {repo_path} in porcelain v2 format. Returns staged, unstaged and untracked entries plus the current branch. Never modifies anything.",
				StatusInput{}, doStatus, wickdocs.Docs{}),
			connector.Op("log", "Log",
				"Read commit history at {repo_path}. Returns one line per commit: hash, author, ISO date, subject. Defaults to 20 commits — raise {limit} only when a wider window is needed.",
				LogInput{}, doLog, wickdocs.Docs{}),
			connector.Op("diff", "Diff",
				"Compare {ref_a} against {ref_b} (or the working tree when {ref_b} is empty) at {repo_path}. Set {stat_only} for a file summary instead of a full patch, which is much smaller.",
				DiffInput{}, doDiff, wickdocs.Docs{}),
			connector.Op("branch_list", "List Branches",
				"List branches at {repo_path} with each branch's short commit and last commit date. Set {remote} to list remote-tracking branches instead of local ones.",
				BranchListInput{}, doBranchList, wickdocs.Docs{}),
			connector.Op("show", "Show Commit",
				"Show commit {ref} at {repo_path} with its changed-file summary. Returns the commit message plus per-file statistics, not the full patch.",
				ShowInput{}, doShow, wickdocs.Docs{}),
			connector.Op("remote_list", "List Remotes",
				"List the remotes of {repo_path}. For each one returns the configured URL with credentials stripped and the URL network operations would actually use, so an SSH-to-HTTPS conversion is visible before anything is pushed.",
				RemoteListInput{}, doRemoteList, wickdocs.Docs{}),
			connector.Op("ls_remote", "Probe Remote",
				"List the branches a remote advertises for {repo_path}, without fetching or changing anything. The cheapest way to verify that the credential and the remote URL both work.",
				LsRemoteInput{}, doLsRemote, wickdocs.Docs{}),
		),
	}
}
```

Add `"github.com/yogasw/wick/pkg/wickdocs"` to `connector.go` imports.

- [ ] **Step 8: Write the integration test for read operations**

```go
func TestReadOpsAgainstTempRepo(t *testing.T) {
	if _, err := ResolveGit(); err != nil {
		t.Skip("git not installed")
	}
	dir := initTestRepo(t)

	// A status run through the real pipeline: policy compiled, args validated,
	// process spawned, envelope returned.
	res, err := Run(context.Background(),
		Cmd{RepoPath: dir, UserArgs: []string{"status", "--porcelain=v2", "--branch"}},
		RunOpts{Timeout: 30 * time.Second, MaxOutput: 1 << 20})
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(res.Stdout, "branch.head") {
		t.Errorf("status output missing branch.head:\n%s", res.Stdout)
	}
}
```

- [ ] **Step 9: Run the full suite**

Run: `go test ./plugins/connector/git/ -v`
Expected: PASS

- [ ] **Step 10: Commit**

```bash
git add plugins/connector/git/connector.go plugins/connector/git/service.go \
        plugins/connector/git/service_test.go
git commit -m "feat(git): read operations with policy-aware envelope"
```

---

### Task 9: Mutating and destructive operations

**Files:**
- Modify: `plugins/connector/git/connector.go`
- Modify: `plugins/connector/git/service_test.go`

**Interfaces:**
- Consumes: `execute`, `envelope`, `currentBranch`, `remoteURL`, `injectedArgs` from Task 8.
- Produces: Input structs and handlers for `branch_create`, `checkout`, `add`, `commit`, `stash`, `fetch`, `pull`, `tag`, `push`, `merge`, `reset`, `rebase`, `clone`, `stash_drop`, `tag_delete`, `raw`; extended `Operations()`.

- [ ] **Step 1: Write the failing test for network-op argument assembly**

```go
func TestPushArgsUseExplicitURL(t *testing.T) {
	// The push argument builder must pass the converted URL explicitly rather
	// than the remote name, so credentials baked into .git/config are bypassed.
	args := buildPushArgs("https://github.com/org/repo.git", "fix/login", false, true)
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "https://github.com/org/repo.git") {
		t.Errorf("push args must carry the explicit URL, got: %s", joined)
	}
	if strings.Contains(joined, " origin ") {
		t.Errorf("push args must not reference the remote name, got: %s", joined)
	}
	if !strings.Contains(joined, "refs/heads/fix/login") {
		t.Errorf("push args must use a full refspec, got: %s", joined)
	}
	if !strings.Contains(joined, "--set-upstream") {
		t.Errorf("set_upstream requested but missing, got: %s", joined)
	}
}

func TestPushArgsForceUsesLease(t *testing.T) {
	args := buildPushArgs("https://abc.com/org/repo.git", "fix/x", true, false)
	joined := strings.Join(args, " ")
	// --force-with-lease refuses to overwrite work the local clone has not seen,
	// which is the safe form of a force push.
	if !strings.Contains(joined, "--force-with-lease") {
		t.Errorf("force push must use --force-with-lease, got: %s", joined)
	}
	if strings.Contains(joined, "--force ") || strings.HasSuffix(joined, "--force") {
		t.Errorf("bare --force must not be used, got: %s", joined)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugins/connector/git/ -run TestPushArgs -v`
Expected: FAIL — `undefined: buildPushArgs`

- [ ] **Step 3: Implement `buildPushArgs` in `service.go`**

```go
// buildPushArgs assembles a push that names the remote URL explicitly. Passing
// the URL rather than "origin" is what makes credentials embedded in
// .git/config irrelevant — git uses the URL given here and our askpass helper.
//
// Force always means --force-with-lease: it refuses to overwrite commits the
// local clone has not seen, which is the difference between recovering from a
// bad rebase and destroying a colleague's work.
func buildPushArgs(url, branch string, force, setUpstream bool) []string {
	args := []string{"push"}
	if force {
		args = append(args, "--force-with-lease")
	}
	if setUpstream {
		args = append(args, "--set-upstream")
	}
	return append(args, url, "HEAD:refs/heads/"+branch)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugins/connector/git/ -run TestPushArgs -v`
Expected: PASS

- [ ] **Step 5: Add mutating Input structs and handlers to `connector.go`**

```go
// BranchCreateInput creates a branch. The name is checked against the policy's
// branch pattern and must not be a protected branch.
type BranchCreateInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Name     string `wick:"required;desc=New branch name. Must satisfy the connector's branch pattern. Example: fix/login-timeout"`
	FromRef  string `wick:"desc=Base commit or branch. Default: the current HEAD."`
	Checkout bool   `wick:"desc=Switch to the new branch after creating it."`
}

// CheckoutInput switches refs, optionally creating the branch.
type CheckoutInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Ref      string `wick:"required;desc=Branch, tag or commit to switch to."`
	Create   bool   `wick:"desc=Create the branch if it does not exist. The branch pattern then applies."`
}

// AddInput stages paths.
type AddInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Paths    string `wick:"required;desc=Comma-separated paths to stage, relative to the repository root. Use . to stage everything."`
}

// CommitInput records staged changes. The current branch must not be protected.
type CommitInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Message  string `wick:"required;textarea;desc=Commit message."`
	All      bool   `wick:"desc=Stage every tracked modified file before committing."`
	DryRun   bool   `wick:"desc=Evaluate the policy and assemble the command without running it."`
}

// StashInput saves or restores work in progress. Dropping a stash is a separate
// operation because it cannot be undone.
type StashInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Action   string `wick:"required;dropdown=push|pop|list;desc=push saves the working tree, pop restores the most recent entry, list shows entries."`
	Message  string `wick:"desc=Label for the stash entry. Used with push."`
}

// FetchInput updates remote-tracking refs.
type FetchInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Remote   string `wick:"default=origin;desc=Remote name. Default: origin."`
	Prune    bool   `wick:"desc=Delete remote-tracking refs whose upstream branch is gone."`
}

// PullInput fetches and integrates.
type PullInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Remote   string `wick:"default=origin;desc=Remote name. Default: origin."`
	Branch   string `wick:"desc=Branch to pull. Default: the current branch's upstream."`
	Rebase   bool   `wick:"desc=Rebase local commits onto the fetched head instead of merging."`
}

// TagInput creates a tag.
type TagInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Name     string `wick:"required;desc=Tag name. Example: v1.2.0"`
	Ref      string `wick:"desc=Commit to tag. Default: HEAD."`
	Message  string `wick:"desc=Annotation message. Supplying it creates an annotated tag instead of a lightweight one."`
}

// PushInput publishes commits. The target branch must not be protected, and
// force requires the policy to allow it.
type PushInput struct {
	RepoPath    string `wick:"required;desc=Absolute path to the repository."`
	Remote      string `wick:"default=origin;desc=Remote name whose URL is used. Default: origin."`
	Branch      string `wick:"desc=Target branch. Default: the current branch."`
	Force       bool   `wick:"desc=Force push using --force-with-lease. Requires allow_force_push in the policy."`
	SetUpstream bool   `wick:"desc=Record the remote branch as the upstream of the current branch."`
	DryRun      bool   `wick:"desc=Evaluate the policy and assemble the command without running it."`
}

// MergeInput integrates another ref into the current branch.
type MergeInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Ref      string `wick:"desc=Branch or commit to merge in. Required unless {abort} is set."`
	NoFF     bool   `wick:"desc=Always create a merge commit, even when a fast-forward is possible."`
	Abort    bool   `wick:"desc=Abort a merge that stopped on conflicts and restore the previous state."`
}

// ResetInput moves HEAD. A hard reset discards work, so it requires the same
// opt-in as a force push.
type ResetInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Mode     string `wick:"required;dropdown=soft|mixed|hard;desc=soft keeps the index and working tree, mixed resets the index, hard discards all local changes. hard requires allow_force_push."`
	Ref      string `wick:"required;desc=Commit to reset to. Example: HEAD~1"`
}

// RebaseInput replays commits. Never interactive — an editor would hang.
type RebaseInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Onto     string `wick:"desc=Branch or commit to rebase onto. Required unless {abort} or {continue_} is set."`
	Abort    bool   `wick:"desc=Abort an in-progress rebase and restore the previous state."`
	Continue bool   `wick:"key=continue_;desc=Continue a rebase after conflicts were resolved and staged."`
}

// CloneInput copies a remote repository to disk.
type CloneInput struct {
	URL    string `wick:"required;desc=Repository URL. An SSH URL is converted to HTTPS when the connector allows it."`
	Dest   string `wick:"required;desc=Absolute destination directory. Must not already exist."`
	Branch string `wick:"desc=Branch to check out. Default: the remote's default branch."`
	Depth  int    `wick:"desc=Create a shallow clone with this many commits of history. Omit for a full clone."`
}

// StashDropInput deletes a stash entry permanently.
type StashDropInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Ref      string `wick:"default=stash@{0};desc=Stash entry to delete. Default: the most recent one."`
}

// TagDeleteInput removes a tag, optionally on the remote too.
type TagDeleteInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Name     string `wick:"required;desc=Tag to delete."`
	Remote   string `wick:"desc=Also delete the tag on this remote. Leave empty to delete locally only."`
}

// RawInput runs an arbitrary git subcommand, gated by the raw rules.
type RawInput struct {
	RepoPath string `wick:"required;desc=Absolute path to the repository."`
	Args     string `wick:"required;desc=Arguments after the word git. Example: bisect start. The subcommand must be allowed by the connector's raw rules."`
	DryRun   bool   `wick:"desc=Evaluate the policy and assemble the command without running it."`
}

func doBranchCreate(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	name := strings.TrimSpace(c.Input("name"))
	return execute(c, "branch_create", repo,
		Request{Branch: name, NewBranch: true},
		func(EffectivePolicy) ([]string, error) {
			if name == "" {
				return nil, errors.New("name is required")
			}
			verb := "branch"
			if c.InputBool("checkout") {
				verb = "checkout"
			}
			args := []string{verb}
			if verb == "checkout" {
				args = append(args, "-b")
			}
			args = append(args, name)
			if from := strings.TrimSpace(c.Input("from_ref")); from != "" {
				args = append(args, from)
			}
			return args, nil
		}, false)
}

func doCheckout(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	ref := strings.TrimSpace(c.Input("ref"))
	create := c.InputBool("create")
	return execute(c, "checkout", repo,
		Request{Branch: ref, NewBranch: create},
		func(EffectivePolicy) ([]string, error) {
			if ref == "" {
				return nil, errors.New("ref is required")
			}
			args := []string{"checkout"}
			if create {
				args = append(args, "-b")
			}
			return append(args, ref), nil
		}, false)
}

func doAdd(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	return execute(c, "add", repo,
		Request{Branch: currentBranch(c, repo)},
		func(EffectivePolicy) ([]string, error) {
			paths := splitCSV(c.Input("paths"))
			if len(paths) == 0 {
				return nil, errors.New("paths is required")
			}
			return append([]string{"add", "--"}, paths...), nil
		}, false)
}

func doCommit(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	branch := currentBranch(c, repo)
	msg := strings.TrimSpace(c.Input("message"))

	if c.InputBool("dry_run") {
		return dryRun(c, "commit", repo, Request{Branch: branch},
			append([]string{"commit"}, "-m", msg))
	}
	return execute(c, "commit", repo, Request{Branch: branch},
		func(EffectivePolicy) ([]string, error) {
			if msg == "" {
				return nil, errors.New("message is required")
			}
			args := []string{"commit", "-m", msg}
			if c.InputBool("all") {
				args = append(args, "--all")
			}
			return args, nil
		}, false)
}

func doStash(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	return execute(c, "stash", repo,
		Request{Branch: currentBranch(c, repo)},
		func(EffectivePolicy) ([]string, error) {
			action := strings.TrimSpace(c.Input("action"))
			switch action {
			case "push":
				args := []string{"stash", "push"}
				if m := strings.TrimSpace(c.Input("message")); m != "" {
					args = append(args, "-m", m)
				}
				return args, nil
			case "pop":
				return []string{"stash", "pop"}, nil
			case "list":
				return []string{"stash", "list"}, nil
			default:
				return nil, fmt.Errorf("action %q is not one of push, pop, list", action)
			}
		}, false)
}

func doFetch(c *connector.Ctx) (any, error) {
	return networkOp(c, "fetch", func(url string) []string {
		args := []string{"fetch"}
		if c.InputBool("prune") {
			args = append(args, "--prune")
		}
		return append(args, url)
	})
}

func doPull(c *connector.Ctx) (any, error) {
	return networkOp(c, "pull", func(url string) []string {
		args := []string{"pull"}
		if c.InputBool("rebase") {
			args = append(args, "--rebase")
		}
		args = append(args, url)
		if b := strings.TrimSpace(c.Input("branch")); b != "" {
			args = append(args, b)
		}
		return args
	})
}

func doTag(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	return execute(c, "tag", repo,
		Request{Branch: currentBranch(c, repo)},
		func(EffectivePolicy) ([]string, error) {
			name := strings.TrimSpace(c.Input("name"))
			if name == "" {
				return nil, errors.New("name is required")
			}
			args := []string{"tag"}
			if m := strings.TrimSpace(c.Input("message")); m != "" {
				args = append(args, "-a", name, "-m", m)
			} else {
				args = append(args, name)
			}
			if ref := strings.TrimSpace(c.Input("ref")); ref != "" {
				args = append(args, ref)
			}
			return args, nil
		}, false)
}

func doPush(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	if err := ValidateRepoPath(repo); err != nil {
		return nil, err
	}
	remote := firstNonEmpty(strings.TrimSpace(c.Input("remote")), "origin")
	branch := firstNonEmpty(strings.TrimSpace(c.Input("branch")), currentBranch(c, repo))
	if branch == "" {
		return nil, errors.New("branch is required (HEAD is detached, so there is no current branch)")
	}

	info, err := ConvertRemote(remoteURL(c, repo, remote),
		ParseHostMap(c.Cfg("remote_host_map")), c.CfgBool("convert_ssh_remote_to_https"))
	if err != nil {
		return nil, err
	}

	pol := policyFor(c, repo, info.Slug)
	force := c.InputBool("force")
	v := pol.Evaluate(Request{Op: "push", Branch: branch, Remote: remote, Force: force})
	if !v.Allow {
		return deniedEnvelope(v, "git push "+remote+" "+branch), nil
	}

	userArgs := buildPushArgs(info.Effective, branch, force, c.InputBool("set_upstream"))
	if err := ValidateUserArgs(userArgs); err != nil {
		return nil, err
	}
	o := runOpts(c, true)
	if c.InputBool("dry_run") {
		return envelope(Result{OK: true,
			Command: mask("git "+strings.Join(userArgs, " "), o.Masks),
			Stdout:  "dry run: the policy allows this command; nothing was executed",
		}, v, &info), nil
	}

	res, err := Run(c.Context(), Cmd{
		RepoPath:     repo,
		InjectedArgs: injectedArgs(c, o.Auth, "push"),
		UserArgs:     userArgs,
		Network:      true,
	}, o)
	if err != nil {
		return nil, err
	}
	return envelope(res, v, &info), nil
}

func doMerge(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	return execute(c, "merge", repo,
		Request{Branch: currentBranch(c, repo)},
		func(EffectivePolicy) ([]string, error) {
			if c.InputBool("abort") {
				return []string{"merge", "--abort"}, nil
			}
			ref := strings.TrimSpace(c.Input("ref"))
			if ref == "" {
				return nil, errors.New("ref is required unless abort is set")
			}
			args := []string{"merge", "--no-edit"}
			if c.InputBool("no_ff") {
				args = append(args, "--no-ff")
			}
			return append(args, ref), nil
		}, false)
}

func doReset(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	mode := strings.TrimSpace(c.Input("mode"))
	// A hard reset discards committed and uncommitted work, so it needs the same
	// explicit opt-in as a force push.
	return execute(c, "reset", repo,
		Request{Branch: currentBranch(c, repo), Force: mode == "hard"},
		func(EffectivePolicy) ([]string, error) {
			ref := strings.TrimSpace(c.Input("ref"))
			if ref == "" {
				return nil, errors.New("ref is required")
			}
			switch mode {
			case "soft", "mixed", "hard":
			default:
				return nil, fmt.Errorf("mode %q is not one of soft, mixed, hard", mode)
			}
			return []string{"reset", "--" + mode, ref}, nil
		}, false)
}

func doRebase(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	return execute(c, "rebase", repo,
		Request{Branch: currentBranch(c, repo)},
		func(EffectivePolicy) ([]string, error) {
			switch {
			case c.InputBool("abort"):
				return []string{"rebase", "--abort"}, nil
			case c.InputBool("continue_"):
				return []string{"rebase", "--continue"}, nil
			}
			onto := strings.TrimSpace(c.Input("onto"))
			if onto == "" {
				return nil, errors.New("onto is required unless abort or continue_ is set")
			}
			return []string{"rebase", onto}, nil
		}, false)
}

func doClone(c *connector.Ctx) (any, error) {
	dest := strings.TrimSpace(c.Input("dest"))
	if dest == "" {
		return nil, errors.New("dest is required")
	}
	if _, err := os.Stat(dest); err == nil {
		return nil, fmt.Errorf("dest %q already exists; clone would not be a fresh checkout", dest)
	}
	info, err := ConvertRemote(strings.TrimSpace(c.Input("url")),
		ParseHostMap(c.Cfg("remote_host_map")), c.CfgBool("convert_ssh_remote_to_https"))
	if err != nil {
		return nil, err
	}

	pol := policyFor(c, dest, info.Slug)
	v := pol.Evaluate(Request{Op: "clone", Remote: info.Effective})
	if !v.Allow {
		return deniedEnvelope(v, "git clone "+info.Effective), nil
	}

	args := []string{"clone"}
	if b := strings.TrimSpace(c.Input("branch")); b != "" {
		args = append(args, "--branch", b)
	}
	if d := c.InputInt("depth"); d > 0 {
		args = append(args, "--depth", strconv.Itoa(d))
	}
	args = append(args, info.Effective, dest)

	o := runOpts(c, true)
	// Clone has no existing repo to run inside, so the working directory is the
	// destination's parent rather than a repo path.
	res, err := Run(c.Context(), Cmd{
		RepoPath:     filepath.Dir(dest),
		InjectedArgs: injectedArgs(c, o.Auth, "clone"),
		UserArgs:     args,
		Network:      true,
	}, o)
	if err != nil {
		return nil, err
	}
	return envelope(res, v, &info), nil
}

func doStashDrop(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	return execute(c, "stash_drop", repo,
		Request{Branch: currentBranch(c, repo)},
		func(EffectivePolicy) ([]string, error) {
			ref := firstNonEmpty(strings.TrimSpace(c.Input("ref")), "stash@{0}")
			return []string{"stash", "drop", ref}, nil
		}, false)
}

func doTagDelete(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	name := strings.TrimSpace(c.Input("name"))
	remote := strings.TrimSpace(c.Input("remote"))

	if remote == "" {
		return execute(c, "tag_delete", repo, Request{},
			func(EffectivePolicy) ([]string, error) {
				if name == "" {
					return nil, errors.New("name is required")
				}
				return []string{"tag", "-d", name}, nil
			}, false)
	}

	// Deleting a remote tag is a network mutation: push an empty ref.
	if err := ValidateRepoPath(repo); err != nil {
		return nil, err
	}
	info, err := ConvertRemote(remoteURL(c, repo, remote),
		ParseHostMap(c.Cfg("remote_host_map")), c.CfgBool("convert_ssh_remote_to_https"))
	if err != nil {
		return nil, err
	}
	pol := policyFor(c, repo, info.Slug)
	v := pol.Evaluate(Request{Op: "tag_delete", Remote: remote})
	if !v.Allow {
		return deniedEnvelope(v, "git push "+remote+" :refs/tags/"+name), nil
	}
	o := runOpts(c, true)
	res, err := Run(c.Context(), Cmd{
		RepoPath:     repo,
		InjectedArgs: injectedArgs(c, o.Auth, "tag_delete"),
		UserArgs:     []string{"push", info.Effective, ":refs/tags/" + name},
		Network:      true,
	}, o)
	if err != nil {
		return nil, err
	}
	return envelope(res, v, &info), nil
}

func doRaw(c *connector.Ctx) (any, error) {
	repo := c.Input("repo_path")
	if err := ValidateRepoPath(repo); err != nil {
		return nil, err
	}
	args := SplitRawArgs(c.Input("args"))
	if len(args) == 0 {
		return nil, errors.New("args is required")
	}
	sub := RawSubcommandOf(args)

	pol := policyFor(c, repo, RepoSlug(remoteURL(c, repo, "origin")))
	v := pol.Evaluate(Request{Op: "raw", RawSubcommand: sub})
	if !v.Allow {
		return deniedEnvelope(v, "git "+strings.Join(args, " ")), nil
	}
	if err := ValidateUserArgs(args); err != nil {
		return nil, err
	}

	o := runOpts(c, true) // a raw subcommand may reach the network; use the longer budget
	if c.InputBool("dry_run") {
		return envelope(Result{OK: true,
			Command: mask("git "+strings.Join(args, " "), o.Masks),
			Stdout:  "dry run: the policy allows this command; nothing was executed",
		}, v, nil), nil
	}
	res, err := Run(c.Context(), Cmd{
		RepoPath:     repo,
		InjectedArgs: injectedArgs(c, o.Auth, "raw"),
		UserArgs:     args,
		Network:      true,
	}, o)
	if err != nil {
		return nil, err
	}
	return envelope(res, v, nil), nil
}

// networkOp is the shared shape for fetch and pull: resolve the remote, evaluate,
// then run with the converted URL.
func networkOp(c *connector.Ctx, op string, build func(url string) []string) (any, error) {
	repo := c.Input("repo_path")
	if err := ValidateRepoPath(repo); err != nil {
		return nil, err
	}
	remote := firstNonEmpty(strings.TrimSpace(c.Input("remote")), "origin")
	info, err := ConvertRemote(remoteURL(c, repo, remote),
		ParseHostMap(c.Cfg("remote_host_map")), c.CfgBool("convert_ssh_remote_to_https"))
	if err != nil {
		return nil, err
	}
	pol := policyFor(c, repo, info.Slug)
	v := pol.Evaluate(Request{Op: op, Branch: currentBranch(c, repo), Remote: remote})
	if !v.Allow {
		return deniedEnvelope(v, "git "+op+" "+remote), nil
	}
	userArgs := build(info.Effective)
	if err := ValidateUserArgs(userArgs); err != nil {
		return nil, err
	}
	o := runOpts(c, true)
	res, err := Run(c.Context(), Cmd{
		RepoPath:     repo,
		InjectedArgs: injectedArgs(c, o.Auth, op),
		UserArgs:     userArgs,
		Network:      true,
	}, o)
	if err != nil {
		return nil, err
	}
	return envelope(res, v, &info), nil
}

// dryRun evaluates the policy and reports the command without running it.
func dryRun(c *connector.Ctx, op, repoPath string, req Request, userArgs []string) (any, error) {
	if err := ValidateRepoPath(repoPath); err != nil {
		return nil, err
	}
	req.Op = op
	pol := policyFor(c, repoPath, RepoSlug(remoteURL(c, repoPath, "origin")))
	v := pol.Evaluate(req)
	o := runOpts(c, false)
	if !v.Allow {
		return deniedEnvelope(v, mask("git "+strings.Join(userArgs, " "), o.Masks)), nil
	}
	return envelope(Result{OK: true,
		Command: mask("git "+strings.Join(userArgs, " "), o.Masks),
		Stdout:  "dry run: the policy allows this command; nothing was executed",
	}, v, nil), nil
}
```

Add `"os"`, `"path/filepath"`, `"fmt"` to `connector.go` imports.

- [ ] **Step 6: Extend `Operations()` with the remaining categories**

```go
		connector.Cat("Branches and Commits", "Create branches, stage and record changes.",
			connector.Op("branch_create", "Create Branch",
				"Create branch {name} at {repo_path}, optionally from {from_ref}. The name must satisfy the connector's branch pattern and must not be a protected branch — both are enforced before git runs.",
				BranchCreateInput{}, doBranchCreate, wickdocs.Docs{}),
			connector.Op("checkout", "Checkout",
				"Switch {repo_path} to {ref}. With {create} set, the branch is created first and the branch pattern applies. Fails if the working tree has conflicting changes.",
				CheckoutInput{}, doCheckout, wickdocs.Docs{}),
			connector.Op("add", "Stage Paths",
				"Stage {paths} in {repo_path} for the next commit. Accepts a comma-separated list; use . to stage everything.",
				AddInput{}, doAdd, wickdocs.Docs{}),
			connector.Op("commit", "Commit",
				"Record staged changes at {repo_path} with {message}. Blocked when the current branch is protected. Set {dry_run} to see the command and the policy verdict without committing.",
				CommitInput{}, doCommit, wickdocs.Docs{}),
			connector.Op("stash", "Stash",
				"Save, restore or list work in progress at {repo_path}. push saves the working tree, pop restores the most recent entry, list shows entries. Deleting an entry is the separate stash_drop operation.",
				StashInput{}, doStash, wickdocs.Docs{}),
			connector.Op("tag", "Create Tag",
				"Create tag {name} at {repo_path}, annotated when {message} is supplied. Local only — pushing tags is a push operation.",
				TagInput{}, doTag, wickdocs.Docs{}),
		),
		connector.Cat("Network", "Exchange commits with a remote.",
			connector.Op("fetch", "Fetch",
				"Update remote-tracking refs for {remote} at {repo_path} without touching the working tree. Uses the connector's credential and the remote's HTTPS URL, ignoring any credentials stored in .git/config.",
				FetchInput{}, doFetch, wickdocs.Docs{}),
			connector.Op("pull", "Pull",
				"Fetch {remote} and integrate it into the current branch at {repo_path}, rebasing instead of merging when {rebase} is set. Blocked when the current branch is protected.",
				PullInput{}, doPull, wickdocs.Docs{}),
		),
		connector.Cat("Destructive", "Operations that publish or discard work. Each is off by default on a new instance.",
			connector.OpDestructive("push", "Push",
				"Publish commits from {repo_path} to {branch} on {remote}. Blocked when the target branch is protected; {force} additionally requires allow_force_push and always uses --force-with-lease. Set {dry_run} to see the command and verdict without pushing.",
				PushInput{}, doPush, wickdocs.Docs{}),
			connector.OpDestructive("merge", "Merge",
				"Merge {ref} into the current branch at {repo_path}, or abort a conflicted merge with {abort}. Blocked when the current branch is protected. Never opens an editor.",
				MergeInput{}, doMerge, wickdocs.Docs{}),
			connector.OpDestructive("reset", "Reset",
				"Move HEAD at {repo_path} to {ref}. soft keeps the index and working tree, mixed resets the index, hard discards every local change and requires allow_force_push.",
				ResetInput{}, doReset, wickdocs.Docs{}),
			connector.OpDestructive("rebase", "Rebase",
				"Replay the current branch's commits onto {onto} at {repo_path}, or abort or continue an in-progress rebase. Never interactive — an interactive rebase would block on an editor until the timeout.",
				RebaseInput{}, doRebase, wickdocs.Docs{}),
			connector.OpDestructive("clone", "Clone",
				"Clone {url} into {dest}. An SSH URL is converted to HTTPS when the connector allows it; a self-hosted host needs a remote_host_map row. Fails when {dest} already exists.",
				CloneInput{}, doClone, wickdocs.Docs{}),
			connector.OpDestructive("stash_drop", "Drop Stash",
				"Delete stash entry {ref} at {repo_path}. A dropped stash cannot be recovered, which is why this is separate from the stash operation.",
				StashDropInput{}, doStashDrop, wickdocs.Docs{}),
			connector.OpDestructive("tag_delete", "Delete Tag",
				"Delete tag {name} at {repo_path}, and on {remote} as well when it is supplied. Deleting a remote tag is a network mutation others will see.",
				TagDeleteInput{}, doTagDelete, wickdocs.Docs{}),
			connector.OpDestructive("raw", "Raw Git Command",
				"Run an arbitrary git subcommand at {repo_path} with {args}. Requires raw_enabled and an explicit allow rule for that subcommand; an unlisted subcommand is denied. Arguments that could execute shell commands are rejected. Set {dry_run} to check the verdict first.",
				RawInput{}, doRaw, wickdocs.Docs{}),
		),
```

- [ ] **Step 7: Write the integration test for a policy-blocked push**

```go
func TestPushToProtectedBranchIsBlockedBeforeSpawn(t *testing.T) {
	// Evaluate directly: the policy must deny without any process running, so
	// this test needs no git and no network.
	pol := EffectivePolicy{
		Protected:   []string{"main"},
		MatchedRule: "global",
	}
	v := pol.Evaluate(Request{Op: "push", Branch: "main"})
	if v.Allow {
		t.Fatal("push to a protected branch was allowed")
	}
	if !strings.Contains(v.Reason, "protected") {
		t.Errorf("Reason = %q, want it to mention the protected branch", v.Reason)
	}

	env := deniedEnvelope(v, "git push origin main").(map[string]any)
	if env["ok"] != false {
		t.Error("a denied envelope must report ok=false")
	}
	if env["exit_code"] != -1 {
		t.Errorf("exit_code = %v, want -1 to mark that nothing ran", env["exit_code"])
	}
}
```

- [ ] **Step 8: Run the full suite and vet**

Run: `go test ./plugins/connector/git/ -v && go vet ./plugins/connector/git/`
Expected: PASS, no vet output

- [ ] **Step 9: Commit**

```bash
git add plugins/connector/git/connector.go plugins/connector/git/service.go \
        plugins/connector/git/service_test.go
git commit -m "feat(git): mutating and destructive operations with explicit remote URLs"
```

---

### Task 10: Plugin entry point with askpass mode

**Files:**
- Create: `plugins/connector/git/main.go`
- Create: `plugins/connector/git/VERSION`
- Test: `plugins/connector/git/main_test.go`

**Interfaces:**
- Consumes: `Module()` from Task 7, `envAskpassUser` / `envAskpassToken` from Task 5.
- Produces: `func main()`, `func askpassReply(prompt string) string`

**Why one binary serves both roles:** git needs an executable to call for
credentials. Writing a helper script would put a file on disk, which the design
rules out. Re-invoking this same binary with `--askpass` gives git an executable
that already exists.

- [ ] **Step 1: Write the failing test for `askpassReply`**

```go
package main

import (
	"os"
	"testing"
)

func TestAskpassReply(t *testing.T) {
	t.Setenv(envAskpassUser, "x-access-token")
	t.Setenv(envAskpassToken, "secret")

	cases := map[string]string{
		"Username for 'https://github.com': ": "x-access-token",
		"Password for 'https://github.com': ": "secret",
		"password:":                           "secret",
		"USERNAME:":                           "x-access-token",
		// git asks other things too; an empty reply is better than leaking the
		// token in answer to a prompt we do not understand.
		"Are you sure you want to continue connecting?": "",
	}
	for prompt, want := range cases {
		if got := askpassReply(prompt); got != want {
			t.Errorf("askpassReply(%q) = %q, want %q", prompt, got, want)
		}
	}
}

func TestAskpassReplyWithoutEnvReturnsEmpty(t *testing.T) {
	os.Unsetenv(envAskpassUser)
	os.Unsetenv(envAskpassToken)
	if got := askpassReply("Password: "); got != "" {
		t.Errorf("askpassReply = %q, want empty when no credential is configured", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugins/connector/git/ -run TestAskpass -v`
Expected: FAIL — `undefined: askpassReply`

- [ ] **Step 3: Write `main.go`**

```go
// Command git serves the git CLI connector as a wick plugin.
//
// main has two modes. Normally it serves the connector over gRPC. When invoked
// with --askpass it acts as git's credential helper: git runs it, it prints the
// username or token from the environment, and exits. That second mode is why the
// credential never needs a file on disk — git requires an executable to call, and
// this binary already is one.
package main

import (
	"fmt"
	"os"
	"strings"

	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

func main() {
	// git invokes GIT_ASKPASS with the prompt as argv[1].
	if len(os.Args) > 1 && os.Args[1] == "--askpass" {
		prompt := ""
		if len(os.Args) > 2 {
			prompt = os.Args[2]
		}
		fmt.Println(askpassReply(prompt))
		return
	}
	wickplugin.Serve(Module())
}

// askpassReply answers a git credential prompt from the environment. An
// unrecognised prompt gets an empty reply rather than the token: git also uses
// askpass for host-key and other confirmations, and answering those with a
// credential would leak it.
func askpassReply(prompt string) string {
	p := strings.ToLower(prompt)
	switch {
	case strings.Contains(p, "username"):
		return os.Getenv(envAskpassUser)
	case strings.Contains(p, "password"), strings.Contains(p, "token"):
		return os.Getenv(envAskpassToken)
	default:
		return ""
	}
}
```

- [ ] **Step 4: Write `VERSION`**

```
0.1.0
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./plugins/connector/git/ -run TestAskpass -v`
Expected: PASS

- [ ] **Step 6: Verify the plugin builds and its manifest dumps**

Run: `go build -o /tmp/git-plugin ./plugins/connector/git/ && /tmp/git-plugin --dump-manifest`
Expected: JSON manifest with `"key": "git"` and every operation listed

On Windows use: `go build -o $env:TEMP\git-plugin.exe ./plugins/connector/git/; & $env:TEMP\git-plugin.exe --dump-manifest`

- [ ] **Step 7: Verify the askpass mode answers correctly**

Run: `WICK_GIT_TOKEN=abc123 WICK_GIT_USERNAME=x-access-token /tmp/git-plugin --askpass "Password for 'https://github.com': "`
Expected: `abc123`

- [ ] **Step 8: Commit**

```bash
git add plugins/connector/git/main.go plugins/connector/git/main_test.go \
        plugins/connector/git/VERSION
git commit -m "feat(git): plugin entry point with self-askpass credential mode"
```

---

### Task 11: Policy Manager widget

**Files:**
- Create: `plugins/connector/git/policyui.go`
- Test: `plugins/connector/git/policyui_test.go`
- Modify: `plugins/connector/git/connector.go` (register the config-only ops)

**Interfaces:**
- Consumes: `EffectivePolicy`, `Resolve`, `ParseRepoRules`, `policyFor`, `Request`, `Verdict` from Tasks 1–7.
- Produces:
  - `type policyManagerInput struct { Browser, Repo, Op, Branch, RuleJSON string }`
  - `func renderPolicyManager(c *connector.Ctx, sim *simResult) (any, error)`
  - `type simResult struct { Repo, Op, Branch string; V Verdict; Pol EffectivePolicy; Command string }`
  - `func doPolicyManager(c *connector.Ctx) (any, error)`
  - `func doPolicySimulate(c *connector.Ctx) (any, error)`
  - `func doPolicyRuleSave(c *connector.Ctx) (any, error)`
  - `func esc(s string) string`

**Critical styling rule:** use inline `style` with theme CSS variables
(`var(--color-navy-800)`, `var(--color-white-100)`, `var(--color-black-900)`).
Tailwind utility classes are purged from runtime-returned HTML and render
unstyled.

- [ ] **Step 1: Write the failing test for the simulator**

```go
package main

import (
	"regexp"
	"strings"
	"testing"
)

func TestSimulateExplainsDenial(t *testing.T) {
	pol := EffectivePolicy{
		BranchPattern: `^ops/.+$`,
		BranchRe:      regexp.MustCompile(`^ops/.+$`),
		Protected:     []string{"master", "main", "release/*"},
		MatchedRule:   "per-repo → */org/infra",
	}
	v := pol.Evaluate(Request{Op: "push", Branch: "fix/login-bug", NewBranch: true})
	if v.Allow {
		t.Fatal("expected a denial for a branch that violates the pattern")
	}

	html := renderSimulation(simResult{
		Repo: "github.com/org/infra", Op: "push", Branch: "fix/login-bug",
		V: v, Pol: pol, Command: "git push origin fix/login-bug",
	})

	for _, want := range []string{
		"DENIED",
		"per-repo → */org/infra",
		"^ops/.+$",
		"git push origin fix/login-bug",
		"release/*",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("simulation HTML is missing %q\n%s", want, html)
		}
	}
	// Tailwind classes are purged from runtime HTML; inline styles must be used.
	if strings.Contains(html, "class=\"bg-") || strings.Contains(html, "class=\"text-") {
		t.Error("simulation HTML uses Tailwind utility classes, which get purged")
	}
	if !strings.Contains(html, "var(--color-") {
		t.Error("simulation HTML must style with theme CSS variables")
	}
}

func TestSimulateShowsAllowed(t *testing.T) {
	pol := EffectivePolicy{
		BranchPattern: `^(fix|feat)/.+$`,
		BranchRe:      regexp.MustCompile(`^(fix|feat)/.+$`),
		Protected:     []string{"master", "main"},
		MatchedRule:   "global",
	}
	v := pol.Evaluate(Request{Op: "push", Branch: "fix/login-bug"})
	if !v.Allow {
		t.Fatalf("expected allow, got: %s", v.Reason)
	}
	html := renderSimulation(simResult{
		Repo: "github.com/org/api", Op: "push", Branch: "fix/login-bug",
		V: v, Pol: pol, Command: "git push origin fix/login-bug",
	})
	if !strings.Contains(html, "ALLOWED") {
		t.Errorf("expected ALLOWED in the output:\n%s", html)
	}
	if !strings.Contains(html, "global") {
		t.Errorf("expected the matched rule reported:\n%s", html)
	}
}

func TestEscapesUserInput(t *testing.T) {
	// A branch name is user input and lands in HTML; it must be escaped.
	html := renderSimulation(simResult{
		Repo: "x", Op: "push", Branch: `<script>alert(1)</script>`,
		V: Verdict{Allow: false, Reason: "nope", MatchedRule: "global"},
	})
	if strings.Contains(html, "<script>") {
		t.Errorf("unescaped user input in the output:\n%s", html)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugins/connector/git/ -run 'TestSimulate|TestEscapes' -v`
Expected: FAIL — `undefined: renderSimulation`, `undefined: simResult`

- [ ] **Step 3: Write `policyui.go` — simulation rendering**

```go
// policyui.go implements the Policy Rules config widget: an editor for per-repo
// rules plus a simulator that answers "what would happen if" before anything is
// pushed.
//
// The simulator calls the same Resolve and Evaluate the operations use, so it can
// never drift from real behaviour. If it says ALLOWED, the operation is allowed.
//
// All markup is styled with inline CSS variables rather than Tailwind classes:
// the manager's Tailwind build does not scan HTML a connector returns at runtime,
// so utility classes would be purged and the widget would render unstyled.
package main

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// simResult is one simulator run.
type simResult struct {
	Repo    string
	Op      string
	Branch  string
	V       Verdict
	Pol     EffectivePolicy
	Command string
}

// esc escapes text destined for HTML. Every interpolated value goes through it —
// repo names, branch names and policy patterns are all user input.
func esc(s string) string { return html.EscapeString(s) }

// renderSimulation renders the verdict panel.
func renderSimulation(s simResult) string {
	badge, badgeColor := "ALLOWED", "#27B199"
	if !s.V.Allow {
		badge, badgeColor = "DENIED", "var(--color-red-500, #E5484D)"
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<div style="border:1px solid var(--color-navy-200);border-radius:8px;padding:12px;margin-top:8px;background:var(--color-white-100);color:var(--color-black-900)">`)
	fmt.Fprintf(&b, `<div style="font-weight:700;font-size:13px;color:%s;margin-bottom:8px">%s</div>`, badgeColor, badge)

	row := func(label, value string) {
		fmt.Fprintf(&b, `<div style="display:flex;gap:8px;font-size:12px;margin-bottom:4px">`+
			`<div style="min-width:110px;opacity:.7">%s</div><div style="font-family:monospace">%s</div></div>`,
			esc(label), esc(value))
	}
	row("Matched rule", s.V.MatchedRule)
	if s.V.Reason != "" {
		row("Reason", s.V.Reason)
	}
	if s.Command != "" {
		row("Would run", s.Command)
		fmt.Fprintf(&b, `<div style="font-size:11px;opacity:.6;margin:2px 0 8px 118px">not executed</div>`)
	}

	fmt.Fprintf(&b, `<div style="border-top:1px solid var(--color-navy-200);margin-top:8px;padding-top:8px">`)
	fmt.Fprintf(&b, `<div style="font-size:12px;font-weight:600;margin-bottom:4px">Effective rules for this repo</div>`)
	pattern := s.Pol.BranchPattern
	if pattern == "" {
		pattern = "(none — any branch name is accepted)"
	}
	row("branch pattern", pattern)
	protected := strings.Join(s.Pol.Protected, ", ")
	if protected == "" {
		protected = "(none)"
	}
	row("protected", protected)
	force := "denied"
	if s.Pol.AllowForcePush {
		force = "allowed"
	}
	row("force push", force)
	if s.Pol.PolicyErr != "" {
		row("config error", s.Pol.PolicyErr)
	}
	fmt.Fprintf(&b, `</div></div>`)
	return b.String()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugins/connector/git/ -run 'TestSimulate|TestEscapes' -v`
Expected: PASS

- [ ] **Step 5: Write the failing test for the rule editor round-trip**

```go
func TestRuleRowsRoundTrip(t *testing.T) {
	rows := []RepoRule{
		{Repo: "*/org/infra", BranchPattern: `^ops/.+$`, Protected: "master,main", ForcePush: "false"},
		{Repo: "*/org/sandbox", BranchPattern: ".*", Protected: "-", ForcePush: "true"},
	}
	encoded, err := encodeRepoRules(rows)
	if err != nil {
		t.Fatalf("encodeRepoRules: %v", err)
	}
	back, err := ParseRepoRules(encoded)
	if err != nil {
		t.Fatalf("ParseRepoRules: %v", err)
	}
	if len(back) != 2 {
		t.Fatalf("got %d rules, want 2", len(back))
	}
	if back[1].Protected != "-" {
		t.Errorf("Protected = %q, want the clear marker preserved", back[1].Protected)
	}
	if back[0].BranchPattern != `^ops/.+$` {
		t.Errorf("BranchPattern = %q, want it preserved verbatim", back[0].BranchPattern)
	}
}

func TestValidateRuleReportsBadRegex(t *testing.T) {
	warn := validateRule(RepoRule{Repo: "*/org/x", BranchPattern: `^(fix/.+$`})
	if warn == "" {
		t.Fatal("expected a warning for a regex that does not compile")
	}
	if !strings.Contains(warn, "regex") && !strings.Contains(warn, "compile") {
		t.Errorf("warning should explain the regex problem, got: %q", warn)
	}

	if warn := validateRule(RepoRule{Repo: "*/org/x", BranchPattern: `^fix/.+$`}); warn != "" {
		t.Errorf("valid rule produced a warning: %q", warn)
	}

	if warn := validateRule(RepoRule{Repo: ""}); warn == "" {
		t.Error("expected a warning for an empty repo glob")
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./plugins/connector/git/ -run 'TestRuleRows|TestValidateRule' -v`
Expected: FAIL — `undefined: encodeRepoRules`, `undefined: validateRule`

- [ ] **Step 7: Implement `encodeRepoRules` and `validateRule`**

```go
// encodeRepoRules serialises rules back into the kvlist storage format.
func encodeRepoRules(rules []RepoRule) (string, error) {
	rows := make([]map[string]string, 0, len(rules))
	for _, r := range rules {
		rows = append(rows, map[string]string{
			"repo":           r.Repo,
			"branch_pattern": r.BranchPattern,
			"protected":      r.Protected,
			"force_push":     r.ForcePush,
		})
	}
	b, err := json.Marshal(rows)
	if err != nil {
		return "", fmt.Errorf("encode repo rules: %w", err)
	}
	return string(b), nil
}

// validateRule returns a human-readable warning for a rule that cannot work, or
// "" when the rule is sound. Catching a bad regex here is the whole point of the
// widget: the alternative is discovering it when a push is unexpectedly blocked.
func validateRule(r RepoRule) string {
	if strings.TrimSpace(r.Repo) == "" {
		return "repo glob is empty, so this rule can never match"
	}
	if p := strings.TrimSpace(r.BranchPattern); p != "" && p != "-" {
		if _, err := regexpCompile(p); err != nil {
			return "branch pattern is not a valid regex: " + err.Error()
		}
	}
	switch strings.TrimSpace(r.ForcePush) {
	case "", "-", "true", "false":
	default:
		return `force_push must be true, false, empty (inherit) or "-" (inherit)`
	}
	return ""
}
```

Add to `policy.go` (so `policyui.go` does not import `regexp` directly):

```go
// regexpCompile wraps regexp.Compile so callers that only need validation do not
// import regexp themselves.
func regexpCompile(pattern string) (*regexp.Regexp, error) { return regexp.Compile(pattern) }
```

- [ ] **Step 8: Run test to verify it passes**

Run: `go test ./plugins/connector/git/ -run 'TestRuleRows|TestValidateRule' -v`
Expected: PASS

- [ ] **Step 9: Implement the widget ops**

```go
// policyManagerInput drives the widget. Browser carries the field's current
// value by the html= convention; the remaining fields come from named inputs in
// the markup this op returns.
type policyManagerInput struct {
	Browser  string `wick:"desc=Current field value, supplied by the config UI."`
	SimRepo  string `wick:"desc=Repository to simulate against, as a path or host/owner/repo."`
	SimOp    string `wick:"desc=Operation to simulate. Example: push"`
	SimBranch string `wick:"desc=Branch name to simulate."`
	RuleJSON string `wick:"desc=Full replacement set of per-repo rules, as a JSON array."`
}

// doPolicyManager renders the editor plus an empty simulator.
func doPolicyManager(c *connector.Ctx) (any, error) {
	return map[string]any{"html": renderPolicyManager(c, nil)}, nil
}

// doPolicySimulate evaluates one hypothetical operation and re-renders with the
// verdict. It uses policyFor, the same compiler the operations use.
func doPolicySimulate(c *connector.Ctx) (any, error) {
	repo := strings.TrimSpace(c.Input("sim_repo"))
	op := firstNonEmpty(strings.TrimSpace(c.Input("sim_op")), "push")
	branch := strings.TrimSpace(c.Input("sim_branch"))

	global := loadGlobal(c)
	rules, err := ParseRepoRules(c.Cfg("repo_policies"))
	pol := Resolve(global, rules, repo, repo)
	if err != nil {
		pol.PolicyErr = "repo_policies is not valid JSON: " + err.Error()
	}

	// A new branch triggers the name pattern; branch_create and checkout -b are
	// the operations that create one.
	newBranch := op == "branch_create"
	v := pol.Evaluate(Request{Op: op, Branch: branch, NewBranch: newBranch})

	sim := &simResult{
		Repo: repo, Op: op, Branch: branch, V: v, Pol: pol,
		Command: fmt.Sprintf("git %s origin %s", op, branch),
	}
	return map[string]any{"html": renderPolicyManager(c, sim)}, nil
}

// doPolicyRuleSave replaces the per-repo rule set. It returns the new value via
// {fields} so the core writes it to repo_policies, and re-renders with any
// per-rule warnings.
func doPolicyRuleSave(c *connector.Ctx) (any, error) {
	raw := strings.TrimSpace(c.Input("rule_json"))
	if raw == "" {
		raw = "[]"
	}
	rules, err := ParseRepoRules(raw)
	if err != nil {
		return map[string]any{"html": renderNotice("Could not save: " + err.Error(), false)}, nil
	}
	encoded, err := encodeRepoRules(rules)
	if err != nil {
		return map[string]any{"html": renderNotice("Could not save: "+err.Error(), false)}, nil
	}
	return map[string]any{
		"fields": map[string]string{"repo_policies": encoded},
		"html":   renderNotice(fmt.Sprintf("Saved %d rule(s).", len(rules)), true),
	}, nil
}

// renderNotice renders a one-line success or failure message.
func renderNotice(msg string, ok bool) string {
	color := "var(--color-red-500, #E5484D)"
	mark := "✕"
	if ok {
		color, mark = "#27B199", "✓"
	}
	return fmt.Sprintf(`<div style="font-size:12px;color:%s;padding:6px 0">%s %s</div>`,
		color, mark, esc(msg))
}
```

- [ ] **Step 10: Implement `renderPolicyManager`**

```go
// renderPolicyManager renders the whole widget: the global fallback summary, one
// block per per-repo rule, and the simulator form.
//
// The global block is shown first and labelled as the fallback so the
// relationship between it and the overrides is visible in the layout rather than
// only in documentation.
func renderPolicyManager(c *connector.Ctx, sim *simResult) string {
	global := loadGlobal(c)
	rules, parseErr := ParseRepoRules(c.Cfg("repo_policies"))

	var b strings.Builder
	fmt.Fprintf(&b, `<div style="font-family:inherit;color:var(--color-black-900)">`)

	// Global fallback.
	fmt.Fprintf(&b, `<div style="border:1px solid var(--color-navy-200);border-radius:8px;padding:10px;margin-bottom:10px;background:var(--color-white-200)">`)
	fmt.Fprintf(&b, `<div style="font-size:12px;font-weight:700;margin-bottom:6px">GLOBAL — used when no rule below matches</div>`)
	pat := global.BranchPattern
	if pat == "" {
		pat = "(none)"
	}
	fmt.Fprintf(&b, `<div style="font-size:12px;font-family:monospace;opacity:.85">branch pattern: %s</div>`, esc(pat))
	prot := strings.Join(global.Protected, ", ")
	if prot == "" {
		prot = "(none)"
	}
	fmt.Fprintf(&b, `<div style="font-size:12px;font-family:monospace;opacity:.85">protected: %s</div>`, esc(prot))
	force := "denied"
	if global.AllowForcePush {
		force = "allowed"
	}
	fmt.Fprintf(&b, `<div style="font-size:12px;font-family:monospace;opacity:.85">force push: %s</div>`, esc(force))
	fmt.Fprintf(&b, `<div style="font-size:11px;opacity:.6;margin-top:6px">Edit these in the Branch Policy section above.</div>`)
	fmt.Fprintf(&b, `</div>`)

	if parseErr != nil {
		fmt.Fprint(&b, renderNotice("repo_policies is not valid JSON: "+parseErr.Error(), false))
	}

	// Per-repo rules.
	fmt.Fprintf(&b, `<div style="font-size:12px;font-weight:700;margin:10px 0 6px">PER-REPO RULES (%d)</div>`, len(rules))
	for i, r := range rules {
		warn := validateRule(r)
		border := "var(--color-navy-200)"
		if warn != "" {
			border = "var(--color-red-500, #E5484D)"
		}
		fmt.Fprintf(&b, `<div style="border:1px solid %s;border-radius:8px;padding:10px;margin-bottom:8px">`, border)
		fmt.Fprintf(&b, `<div style="font-size:12px;font-weight:600;margin-bottom:4px">RULE %d — %s <span style="opacity:.6;font-weight:400">(%d wildcard(s))</span></div>`,
			i+1, esc(r.Repo), Specificity(r.Repo))
		line := func(k, v string) {
			if v == "" {
				v = "(inherit global)"
			} else if v == "-" {
				v = "(cleared)"
			}
			fmt.Fprintf(&b, `<div style="font-size:12px;font-family:monospace;opacity:.85">%s: %s</div>`, esc(k), esc(v))
		}
		line("branch pattern", r.BranchPattern)
		line("protected", r.Protected)
		line("force push", r.ForcePush)
		if warn != "" {
			fmt.Fprint(&b, renderNotice(warn, false))
		}
		fmt.Fprintf(&b, `</div>`)
	}

	// Rule editor: a textarea holding the full set, saved in one round-trip.
	current, _ := encodeRepoRules(rules)
	fmt.Fprintf(&b, `<div style="border:1px solid var(--color-navy-200);border-radius:8px;padding:10px;margin-bottom:10px">`)
	fmt.Fprintf(&b, `<div style="font-size:12px;font-weight:600;margin-bottom:6px">Edit rules</div>`)
	fmt.Fprintf(&b, `<textarea name="rule_json" rows="6" style="width:100%%;font-family:monospace;font-size:12px;padding:6px;border:1px solid var(--color-navy-200);border-radius:6px;background:var(--color-white-100);color:var(--color-black-900)">%s</textarea>`, esc(current))
	fmt.Fprintf(&b, `<div style="font-size:11px;opacity:.6;margin:4px 0 6px">Keys: repo (glob), branch_pattern (regex), protected (comma-separated globs, or - to clear), force_push (true/false/empty/-).</div>`)
	fmt.Fprintf(&b, `<button data-op="policy_rule_save" style="font-size:12px;padding:5px 10px;border-radius:6px;border:1px solid #27B199;background:#27B199;color:#fff;cursor:pointer">Save rules</button>`)
	fmt.Fprintf(&b, `</div>`)

	// Simulator.
	fmt.Fprintf(&b, `<div style="border:1px solid var(--color-navy-200);border-radius:8px;padding:10px">`)
	fmt.Fprintf(&b, `<div style="font-size:12px;font-weight:600;margin-bottom:6px">Policy simulator</div>`)
	input := func(name, placeholder, value string) {
		fmt.Fprintf(&b, `<input name="%s" placeholder="%s" value="%s" style="width:100%%;font-size:12px;padding:5px;margin-bottom:5px;border:1px solid var(--color-navy-200);border-radius:6px;background:var(--color-white-100);color:var(--color-black-900)"/>`,
			esc(name), esc(placeholder), esc(value))
	}
	simRepo, simOp, simBranch := "", "push", ""
	if sim != nil {
		simRepo, simOp, simBranch = sim.Repo, sim.Op, sim.Branch
	}
	input("sim_repo", "github.com/org/infra or d:/code/work/api", simRepo)
	input("sim_op", "push", simOp)
	input("sim_branch", "fix/login-bug", simBranch)
	fmt.Fprintf(&b, `<button data-op="policy_simulate" style="font-size:12px;padding:5px 10px;border-radius:6px;border:1px solid var(--color-navy-400);background:transparent;color:var(--color-black-900);cursor:pointer">Simulate</button>`)
	if sim != nil {
		fmt.Fprint(&b, renderSimulation(*sim))
	}
	fmt.Fprintf(&b, `</div></div>`)
	return b.String()
}
```

- [ ] **Step 11: Register the config-only ops in `Operations()`**

Append this category inside `Operations()`:

```go
		connector.Cat("Configuration", "Widgets that back the config form. Not available to agents.",
			connector.OpConfigOnly("policy_manager", "Policy Manager",
				"Render the per-repo policy editor and simulator. Backs the Policy Rules widget in the config form; never called by an agent.",
				policyManagerInput{}, doPolicyManager, wickdocs.Docs{}),
			connector.OpConfigOnly("policy_simulate", "Simulate Policy",
				"Evaluate a hypothetical operation against the current policy and report the verdict, the matched rule and the command that would run. Backs the simulator button.",
				policyManagerInput{}, doPolicySimulate, wickdocs.Docs{}),
			connector.OpConfigOnly("policy_rule_save", "Save Policy Rules",
				"Replace the per-repo rule set from the editor textarea and report per-rule warnings. Backs the Save rules button.",
				policyManagerInput{}, doPolicyRuleSave, wickdocs.Docs{}),
		),
```

- [ ] **Step 12: Write the failing test asserting the widget ops are config-only**

```go
func TestPolicyOpsAreConfigOnly(t *testing.T) {
	var found int
	for _, cat := range Operations() {
		for _, op := range cat.Ops {
			switch op.Key {
			case "policy_manager", "policy_simulate", "policy_rule_save":
				found++
				if !op.ConfigOnly {
					t.Errorf("op %q must be declared with OpConfigOnly so it never reaches the MCP surface", op.Key)
				}
			}
		}
	}
	if found != 3 {
		t.Errorf("found %d policy widget ops, want 3", found)
	}
}

func TestDestructiveOpsAreMarked(t *testing.T) {
	want := map[string]bool{
		"push": true, "merge": true, "reset": true, "rebase": true,
		"clone": true, "stash_drop": true, "tag_delete": true, "raw": true,
	}
	seen := make(map[string]bool)
	for _, cat := range Operations() {
		for _, op := range cat.Ops {
			if want[op.Key] {
				seen[op.Key] = true
				if !op.Destructive {
					t.Errorf("op %q must be declared with OpDestructive so it defaults to off", op.Key)
				}
			}
		}
	}
	for key := range want {
		if !seen[key] {
			t.Errorf("op %q is missing from Operations()", key)
		}
	}
}
```

Field names verified against `pkg/connector/connector.go`: `Operation.Destructive`,
`Operation.ConfigOnly`, `Category.Ops`. Use them as written.

- [ ] **Step 13: Run the full suite**

Run: `go test ./plugins/connector/git/ -v && go vet ./plugins/connector/git/`
Expected: PASS, no vet output

- [ ] **Step 14: Verify the manifest lists every operation**

Run: `go build -o /tmp/git-plugin ./plugins/connector/git/ && /tmp/git-plugin --dump-manifest | grep -c '"key"'`
Expected: a count matching every op plus the connector key

- [ ] **Step 15: Commit**

```bash
git add plugins/connector/git/policyui.go plugins/connector/git/policyui_test.go \
        plugins/connector/git/connector.go plugins/connector/git/policy.go
git commit -m "feat(git): policy manager widget with editor and simulator"
```

---

### Task 12: Documentation

**Files:**
- Create: `docs/connectors/git.md`
- Modify: `docs/connectors/index.md`

**Interfaces:** none — documentation only.

- [ ] **Step 1: Read a sibling page to match structure**

Run: `head -60 docs/connectors/loki.md`
Match its heading structure, frontmatter and tone.

- [ ] **Step 2: Write `docs/connectors/git.md`**

Cover, in this order:
1. What it is — a git CLI wrapper, host-agnostic, distinct from the `github` and `bitbucket` API connectors.
2. Prerequisite — `git` on the PATH of the machine running wick.
3. Setup — identity, HTTPS credential, `auth_method` and why `askpass` is the default.
4. Policy — the two layers, `""` versus `"-"`, and that per-repo cannot un-protect a branch.
5. The Policy Cookbook scenarios from [plan.md](plan.md) §3, verbatim.
6. Operations table — one row per op with its policy gates.
7. What it deliberately does not do — no SSH key auth, no `.git/config` rewriting, no remote execution, with the reason for each.

- [ ] **Step 3: Add the page to the connector index**

Follow the existing pattern in `docs/connectors/index.md`.

- [ ] **Step 4: Verify the docs build**

Run: `npm run docs:build` (from the repo root, if the docs toolchain is installed)
Expected: build succeeds with no dead-link warnings for `git.md`

- [ ] **Step 5: Commit**

```bash
git add docs/connectors/git.md docs/connectors/index.md
git commit -m "docs(git): document the git CLI connector and its policy model"
```

---

## Self-Review

**Spec coverage** — every spec section maps to a task:

| Spec section | Task |
|---|---|
| §1 architecture, four-stage pipeline | 4, 8 (`execute`) |
| §1 `userArgs`/`injectedArgs` split | 4 (structural), 8 (`injectedArgs`) |
| §2 Config struct, all fields | 7 |
| §2 two-layer resolution, `""` vs `"-"`, tie-break | 1 |
| §2 matching semantics (glob vs regex) | 1 (`MatchRepo`, `IsProtected`) |
| §3 Policy Cookbook | 12 (docs, verbatim) |
| §4 read ops | 8 |
| §4 mutating ops | 9 |
| §4 destructive ops incl. `stash_drop`, `tag_delete` | 9 |
| §4 `policy_preview` config-only op | 11 (as `policy_manager` + 2 more) |
| §4 return envelope | 8 (`envelope`) |
| §4 `dry_run` | 9 (`dryRun`, `doPush`, `doRaw`) |
| §5 credential injection, three methods | 5 |
| §5 remote override, never rewrite `.git/config` | 3, 9 (`buildPushArgs`) |
| §5 SSH→HTTPS + two must-fail cases | 3 |
| §5 execution guards (args, env, hooks, prompts) | 4, 5, 8 (`injectedArgs`) |
| §5 runtime limits, process-group kill, masking | 6 |
| §6 Policy Manager widget | 11 |
| §7 testing | every task |
| §8 deferred | 12 (documented) |

**Naming consistency check** — names used across tasks: `Resolve`, `Evaluate`,
`EffectivePolicy`, `GlobalPolicy`, `RepoRule`, `Request`, `Verdict`,
`IsProtected`, `Specificity`, `MatchRepo`, `ParseKVList`, `ParseRepoRules`,
`ParseHostMap`, `ConvertRemote`, `RemoteInfo`, `StripCredentials`, `RepoSlug`,
`Cmd`, `Argv`, `ValidateUserArgs`, `SplitRawArgs`, `RawSubcommandOf`, `AuthSpec`,
`BuildEnv`, `AuthInjectedArgs`, `basicAuthValue`, `Run`, `RunOpts`, `Result`,
`ResolveGit`, `ValidateRepoPath`, `capBytes`, `mask`, `setProcAttr`, `killGroup`,
`envelope`, `deniedEnvelope`, `execute`, `injectedArgs`, `dryRun`, `networkOp`,
`buildPushArgs`, `currentBranch`, `remoteURL`, `emptyHooksDir`, `selfPath`,
`firstNonEmpty`, `splitCSV`, `parseRawRules`, `loadGlobal`, `loadAuth`,
`runOpts`, `policyFor`, `simResult`, `renderSimulation`, `renderPolicyManager`,
`renderNotice`, `encodeRepoRules`, `validateRule`, `regexpCompile`, `esc`,
`askpassReply`. Each is defined exactly once and used consistently.

**API verified against source** (not assumed):

- `Operation.Destructive`, `Operation.ConfigOnly`, `Category.Ops` — confirmed in `pkg/connector/connector.go`.
- `Op` / `OpDestructive` / `OpConfigOnly` are generic over the input type and take `docs wickdocs.Docs` last — confirmed.
- `--dump-manifest` is handled inside `wickplugin.Serve` (`pkg/plugin/serve.go:49`). Task 10's `main.go` intercepts `--askpass` *before* calling `Serve`, so the two flags do not collide.

**One residual risk**, flagged rather than papered over: the `html=<op>` widget's
button convention (`data-op`, named inputs forwarded as `input.<name>`) is
documented in the `config-tags` skill and used by `notion_unofficial`, but Task 11
is the first use of it for a *multi-field editor* rather than a picker. If the
round-trip does not behave as documented, the fallback is to drop the editor
textarea and keep `repo_policies` as a plain `kvlist` field — the simulator
(`policy_simulate`) works either way, since it only reads config and returns HTML.
