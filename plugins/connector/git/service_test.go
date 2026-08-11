package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/safeexec"
)

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

// testCtx builds the Ctx a plugin receives over gRPC. Config values are the only
// input the loaders read, so a map is the whole fixture.
func testCtx(cfg map[string]string) *connector.Ctx {
	return connector.NewPluginCtx(context.Background(), cfg, map[string]string{})
}

func configKeySet(t *testing.T) map[string]bool {
	t.Helper()
	configs := entity.StructToConfigs(Config{})
	if len(configs) == 0 {
		t.Fatal("StructToConfigs(Config{}) returned no rows; the wick tags are not being read")
	}
	have := make(map[string]bool, len(configs))
	for _, cfg := range configs {
		have[cfg.Key] = true
	}
	return have
}

func TestConfigKeysMatchWhatServiceReads(t *testing.T) {
	// entity.StructToConfigs derives snake_case keys. service.go reads those keys
	// by hand, so a rename on either side must fail loudly here.
	have := configKeySet(t)
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

// cfgReadPattern finds every literal config key this package reads. Only literal
// keys can be checked — a computed key would be invisible here, so keep reads
// literal.
var cfgReadPattern = regexp.MustCompile(`c\.(?:Cfg|CfgInt|CfgBool)\("([a-zA-Z0-9_]+)"\)`)

// TestEveryCfgReadIsADeclaredConfigKey scans this package's own sources instead
// of trusting a hand-maintained list. A hardcoded list is exactly as stale as
// whoever last edited it: adding a c.Cfg("new_key") without a matching struct
// field would silently read "" forever and the list-based test above would still
// pass. Deriving the expected set from the source cannot drift.
func TestEveryCfgReadIsADeclaredConfigKey(t *testing.T) {
	have := configKeySet(t)

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	read := map[string][]string{} // key → files reading it
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, rerr := os.ReadFile(filepath.Clean(name))
		if rerr != nil {
			t.Fatalf("read %s: %v", name, rerr)
		}
		for _, m := range cfgReadPattern.FindAllStringSubmatch(string(src), -1) {
			read[m[1]] = append(read[m[1]], name)
		}
	}
	if len(read) == 0 {
		t.Fatal("found no c.Cfg reads in the package; the scan pattern is broken")
	}

	keys := make([]string, 0, len(read))
	for k := range read {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !have[k] {
			t.Errorf("%s reads config key %q, but no Config field declares it "+
				"(StructToConfigs would never seed it, so the read is always empty)",
				strings.Join(read[k], ", "), k)
		}
	}
}

func TestLoadGlobalFromConfig(t *testing.T) {
	c := testCtx(map[string]string{
		"branch_name_pattern": `^(fix|feat)/[a-z0-9._-]+$`,
		"protected_branches":  `[{"branch":"master"},{"branch":" release/* "},{"branch":""}]`,
		"allow_force_push":    "true",
		"raw_enabled":         "true",
		"raw_rules":           `[{"subcommand":"bisect","mode":"allow"}]`,
	})
	g := loadGlobal(c)

	if g.BranchPattern != `^(fix|feat)/[a-z0-9._-]+$` {
		t.Errorf("BranchPattern = %q", g.BranchPattern)
	}
	// An empty branch row is dropped and surrounding whitespace is trimmed, so a
	// hand-typed table cannot produce a glob that never matches.
	if len(g.Protected) != 2 || g.Protected[0] != "master" || g.Protected[1] != "release/*" {
		t.Errorf("Protected = %#v, want [master release/*]", g.Protected)
	}
	if !g.AllowForcePush || !g.RawEnabled {
		t.Errorf("AllowForcePush = %v, RawEnabled = %v, want both true", g.AllowForcePush, g.RawEnabled)
	}
	if g.RawRules["bisect"] != "allow" {
		t.Errorf("RawRules = %v", g.RawRules)
	}
}

func TestLoadGlobalDefaultsAreClosed(t *testing.T) {
	g := loadGlobal(testCtx(map[string]string{}))
	if g.AllowForcePush || g.RawEnabled {
		t.Errorf("an unconfigured instance must not allow force push or raw: %+v", g)
	}
	if len(g.Protected) != 0 || g.BranchPattern != "" {
		t.Errorf("expected no branch policy by default, got %+v", g)
	}
}

func TestLoadAuthDefaultsToAskpass(t *testing.T) {
	a := loadAuth(testCtx(map[string]string{"username": " bot ", "token": "tok-123456"}))
	// askpass is the only method that keeps the token out of argv, so an unset
	// auth_method must land there rather than on a riskier method.
	if a.Method != "askpass" {
		t.Errorf("Method = %q, want askpass", a.Method)
	}
	if a.Username != "bot" {
		t.Errorf("Username = %q, want the trimmed value", a.Username)
	}
	if a.Token != "tok-123456" {
		t.Errorf("Token = %q", a.Token)
	}
}

func TestLoadAuthHonoursConfiguredMethod(t *testing.T) {
	a := loadAuth(testCtx(map[string]string{"auth_method": "extraheader"}))
	if a.Method != "extraheader" {
		t.Errorf("Method = %q, want extraheader", a.Method)
	}
}

func TestPolicyForUsesPerRepoRule(t *testing.T) {
	c := testCtx(map[string]string{
		"protected_branches": `[{"branch":"master"}]`,
		"repo_policies":      `[{"repo":"abc.com/org/*","force_push":"true"}]`,
	})
	p := policyFor(c, "d:/code/work/repo", "abc.com/org/repo")

	if p.MatchedRule != "per-repo → abc.com/org/*" {
		t.Errorf("MatchedRule = %q, want the per-repo rule", p.MatchedRule)
	}
	if !p.AllowForcePush {
		t.Error("the per-repo rule sets force_push=true and must win over the global default")
	}
	if !IsProtected(p, "master") {
		t.Error("the per-repo rule leaves protected empty, so master must stay inherited-protected")
	}
}

func TestPolicyForMalformedRulesFailsClosed(t *testing.T) {
	c := testCtx(map[string]string{"repo_policies": `{"repo":"abc.com/org/repo"}`})
	p := policyFor(c, "d:/code/work/repo", "abc.com/org/repo")

	if p.PolicyErr == "" {
		t.Fatal("malformed repo_policies must record PolicyErr, not be ignored")
	}
	// A broken policy must not become a permissive policy: mutations are refused
	// while reads stay available so the config can be inspected and fixed.
	if v := p.Evaluate(Request{Op: "push", Branch: "fix/x"}); v.Allow {
		t.Error("push must be denied while the policy config is invalid")
	}
	if v := p.Evaluate(Request{Op: "status"}); !v.Allow {
		t.Error("reads must remain allowed while the policy config is invalid")
	}
}

func TestRunOptsTimeouts(t *testing.T) {
	c := testCtx(map[string]string{
		"timeout_seconds":         "30",
		"network_timeout_seconds": "90",
		"max_output_bytes":        "1024",
	})
	if got := runOpts(c, false).Timeout.Seconds(); got != 30 {
		t.Errorf("local timeout = %vs, want 30", got)
	}
	if got := runOpts(c, true).Timeout.Seconds(); got != 90 {
		t.Errorf("network timeout = %vs, want 90", got)
	}
	if got := runOpts(c, false).MaxOutput; got != 1024 {
		t.Errorf("MaxOutput = %d, want 1024", got)
	}
}

func TestRunOptsFallbackTimeouts(t *testing.T) {
	// An unset or nonsense value must not become "no timeout" — a hung git
	// process with no deadline would occupy the worker forever.
	c := testCtx(map[string]string{"timeout_seconds": "0", "network_timeout_seconds": "-5"})
	if got := runOpts(c, false).Timeout.Seconds(); got != 60 {
		t.Errorf("local fallback = %vs, want 60", got)
	}
	if got := runOpts(c, true).Timeout.Seconds(); got != 180 {
		t.Errorf("network fallback = %vs, want 180", got)
	}
	if got := runOpts(c, false).MaxOutput; got != 262144 {
		t.Errorf("MaxOutput fallback = %d, want 262144", got)
	}
}

func TestRunOptsMasksIncludeBase64Credential(t *testing.T) {
	const user, token = "x-access-token", "ghp_secretvalue123"
	c := testCtx(map[string]string{
		"username":    user,
		"token":       token,
		"auth_method": "extraheader",
	})
	o := runOpts(c, true)

	basic := basicAuthValue(user, token)
	var haveRaw, haveB64 bool
	for _, m := range o.Masks {
		switch m {
		case token:
			haveRaw = true
		case basic:
			haveB64 = true
		}
	}
	if !haveRaw {
		t.Errorf("Masks %v is missing the raw token", o.Masks)
	}
	// base64 is encoding, not protection: it decodes in one step. If only the raw
	// token were masked, the extraheader argv recorded in Result.Command — and so
	// the run history — would carry a directly usable credential.
	if !haveB64 {
		t.Errorf("Masks %v is missing basicAuthValue(username, token); the "+
			"extraheader credential would stay readable in the recorded command", o.Masks)
	}
}

func TestRunOptsMasksScrubExtraheaderArgv(t *testing.T) {
	const user, token = "x-access-token", "ghp_secretvalue123"
	c := testCtx(map[string]string{"username": user, "token": token, "auth_method": "extraheader"})
	o := runOpts(c, true)

	// Exercise the real path end to end: the argv the plugin would build, masked
	// with the options runOpts produced.
	argv := Cmd{InjectedArgs: AuthInjectedArgs(o.Auth), UserArgs: []string{"push"}}.Argv()
	shown := mask("git "+strings.Join(argv, " "), o.Masks)

	if strings.Contains(shown, token) {
		t.Errorf("raw token leaked into the recorded command: %s", shown)
	}
	if strings.Contains(shown, basicAuthValue(user, token)) {
		t.Errorf("base64 credential leaked into the recorded command: %s", shown)
	}
}

func TestRunOptsNoTokenNoMasks(t *testing.T) {
	// mask() would ignore short values anyway, but an empty mask list also keeps
	// the placeholder out of output for a public-repo instance.
	if o := runOpts(testCtx(map[string]string{}), false); len(o.Masks) != 0 {
		t.Errorf("Masks = %v, want empty when no token is configured", o.Masks)
	}
}

func TestSelfPathIsAbsolute(t *testing.T) {
	// The value becomes GIT_ASKPASS, which git execs with an unpredictable cwd,
	// so a relative path would silently fail to authenticate.
	if p := selfPath(); !filepath.IsAbs(p) {
		t.Errorf("selfPath() = %q, want an absolute path", p)
	}
}

func TestFirstNonEmpty(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"", "  ", "origin"}, "origin"},
		{[]string{"upstream", "origin"}, "upstream"},
		{[]string{"", ""}, ""},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := firstNonEmpty(tc.in...); got != tc.want {
			t.Errorf("firstNonEmpty(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestModuleShape(t *testing.T) {
	m := Module()
	if m.Meta.Key != Key {
		t.Errorf("Meta.Key = %q, want %q (must equal the plugin folder name)", m.Meta.Key, Key)
	}
	if len(m.Configs) == 0 {
		t.Error("Module.Configs is empty; the Config struct is not being reflected")
	}
	if len(m.Meta.DefaultTags) == 0 {
		t.Error("Module.Meta.DefaultTags is empty; the connector would not be grouped in the UI")
	}
	if m.Operations == nil {
		t.Error("Module.Operations must be non-nil")
	}
	if len(m.Operations) == 0 {
		t.Error("Module.Operations is empty; the read category is not wired in")
	}
}

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
	info := &RemoteInfo{Original: "git@abc.com:org/repo.git",
		Effective: "https://abc.com/org/repo.git", Converted: true}
	m := envelope(Result{OK: true}, Verdict{Allow: true, MatchedRule: "global"}, info).(map[string]any)

	rem, ok := m["remote"].(map[string]any)
	if !ok {
		t.Fatalf("remote is %T, want map[string]any", m["remote"])
	}
	if rem["converted"] != true || rem["effective"] != "https://abc.com/org/repo.git" {
		t.Errorf("remote block = %v, want the converted URL reported", rem)
	}
}

func TestEnvelopeCarriesDenyReason(t *testing.T) {
	m := deniedEnvelope(Verdict{Allow: false, Reason: "branch is protected", MatchedRule: "global"},
		"git push", "push").(map[string]any)

	if m["ok"] != false {
		t.Errorf("ok = %v, want false for a denied operation", m["ok"])
	}
	pol := m["policy"].(map[string]any)
	if pol["verdict"] != "deny" {
		t.Errorf("verdict = %v, want deny", pol["verdict"])
	}
	// Without the reason the agent cannot tell "denied by policy" from "git
	// failed", and cannot know what to change to make the call succeed.
	if pol["reason"] != "branch is protected" {
		t.Errorf("reason = %v, want the verdict's reason", pol["reason"])
	}
	// The reason alone answers only "why did THIS fail". Evaluate stops at the first
	// rule that fires, so a caller working from reasons alone pays one round trip per
	// rule and never learns about a rule it has not yet broken. The refusal has to
	// name the operation that answers in full.
	next, _ := pol["next_step"].(string)
	if !strings.Contains(next, "policy_show") {
		t.Errorf("next_step = %q, want it to point at policy_show", next)
	}
}

// opCtx builds the Ctx a read handler receives: config plus operation input.
func opCtx(cfg, input map[string]string) *connector.Ctx {
	if cfg == nil {
		cfg = map[string]string{}
	}
	return connector.NewPluginCtx(context.Background(), cfg, input)
}

// envOf unwraps a handler's envelope, failing the test on a Go error. Written as
// a one-argument-pair helper so a handler call can be passed straight in:
// envOf(t)(doStatus(ctx)).
func envOf(t *testing.T) func(any, error) map[string]any {
	t.Helper()
	return func(out any, err error) map[string]any {
		t.Helper()
		if err != nil {
			t.Fatalf("handler returned an error: %v", err)
		}
		m, ok := out.(map[string]any)
		if !ok {
			t.Fatalf("handler returned %T, want map[string]any", out)
		}
		return m
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := ResolveGit(); err != nil {
		t.Skip("git not installed")
	}
}

func TestReadOpsAgainstTempRepo(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)

	t.Run("status", func(t *testing.T) {
		m := envOf(t)(doStatus(opCtx(nil, map[string]string{"repo_path": dir})))
		if m["ok"] != true {
			t.Fatalf("status failed: %+v", m)
		}
		if !strings.Contains(m["stdout"].(string), "branch.head") {
			t.Errorf("status output missing branch.head:\n%s", m["stdout"])
		}
		// The verdict travels with every response, success included, so "why did
		// this run" is always answerable from the response alone.
		if pol := m["policy"].(map[string]any); pol["verdict"] != "allow" {
			t.Errorf("policy = %v, want an allow verdict", pol)
		}
	})

	t.Run("log", func(t *testing.T) {
		m := envOf(t)(doLog(opCtx(nil, map[string]string{"repo_path": dir, "limit": "5"})))
		if m["ok"] != true {
			t.Fatalf("log failed: %+v", m)
		}
		if !strings.Contains(m["stdout"].(string), "initial") {
			t.Errorf("log output missing the initial commit subject:\n%s", m["stdout"])
		}
	})

	t.Run("diff", func(t *testing.T) {
		m := envOf(t)(doDiff(opCtx(nil, map[string]string{"repo_path": dir, "stat_only": "true"})))
		if m["ok"] != true {
			t.Fatalf("diff failed: %+v", m)
		}
	})

	t.Run("branch_list", func(t *testing.T) {
		m := envOf(t)(doBranchList(opCtx(nil, map[string]string{"repo_path": dir})))
		if m["ok"] != true {
			t.Fatalf("branch_list failed: %+v", m)
		}
		if !strings.Contains(m["stdout"].(string), "main") {
			t.Errorf("branch_list output missing main:\n%s", m["stdout"])
		}
	})

	t.Run("show", func(t *testing.T) {
		m := envOf(t)(doShow(opCtx(nil, map[string]string{"repo_path": dir, "ref": "HEAD"})))
		if m["ok"] != true {
			t.Fatalf("show failed: %+v", m)
		}
		if !strings.Contains(m["stdout"].(string), "README.md") {
			t.Errorf("show output missing the changed file:\n%s", m["stdout"])
		}
	})

	t.Run("remote_list has no remotes", func(t *testing.T) {
		// A fresh repo has no remotes at all. The op must report an empty list, not
		// fail — "no remotes" is a normal state the agent needs to be able to see.
		m := envOf(t)(doRemoteList(opCtx(nil, map[string]string{"repo_path": dir})))
		if m["ok"] != true {
			t.Fatalf("remote_list failed: %+v", m)
		}
		if got := m["remotes"].([]map[string]any); len(got) != 0 {
			t.Errorf("remotes = %v, want empty for a repo with no remote", got)
		}
	})
}

func TestReadOpsRejectMissingRepoPath(t *testing.T) {
	// Every read op validates the repo path before anything else, so a bad path is
	// a Go error the agent sees verbatim — not a git failure buried in stderr.
	handlers := map[string]connector.ExecuteFunc{
		"status": doStatus, "log": doLog, "diff": doDiff,
		"branch_list": doBranchList, "show": doShow,
		"remote_list": doRemoteList, "ls_remote": doLsRemote,
	}
	for name, h := range handlers {
		t.Run(name, func(t *testing.T) {
			if _, err := h(opCtx(nil, map[string]string{"repo_path": "", "ref": "HEAD"})); err == nil {
				t.Error("an empty repo_path must be rejected")
			}
			if _, err := h(opCtx(nil, map[string]string{
				"repo_path": filepath.Join(t.TempDir(), "nope"), "ref": "HEAD",
			})); err == nil {
				t.Error("a nonexistent repo_path must be rejected")
			}
		})
	}
}

func TestShowRequiresRef(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	if _, err := doShow(opCtx(nil, map[string]string{"repo_path": dir})); err == nil {
		t.Error("show without a ref must be rejected rather than defaulting to HEAD")
	}
}

func TestReadOpsRefusesFlagShapedRef(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)

	// A ref arriving from an agent must never be parsed by git as an option. Every
	// ref-taking read op ends its options first, so "--all" is looked up as a ref
	// name (and fails as one) instead of silently changing what the command does.
	cases := []struct {
		op   string
		h    connector.ExecuteFunc
		in   map[string]string
		want string
	}{
		{"log", doLog, map[string]string{"repo_path": dir, "ref": "--all"}, "--all"},
		{"diff", doDiff, map[string]string{"repo_path": dir, "ref_a": "--cached"}, "--cached"},
		{"show", doShow, map[string]string{"repo_path": dir, "ref": "--all"}, "--all"},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			m := envOf(t)(tc.h(opCtx(nil, tc.in)))
			if m["ok"] == true {
				t.Fatalf("%s accepted a flag-shaped ref as a flag: %+v", tc.op, m)
			}
			cmd := m["command"].(string)
			if !strings.Contains(cmd, "--end-of-options") {
				t.Errorf("%s argv has no --end-of-options guard: %s", tc.op, cmd)
			}
			// The value must sit AFTER the guard, otherwise the guard protects nothing.
			if idx := strings.Index(cmd, "--end-of-options"); idx >= 0 &&
				!strings.Contains(cmd[idx:], tc.want) {
				t.Errorf("%s put %q before --end-of-options: %s", tc.op, tc.want, cmd)
			}
		})
	}
}

func TestDiffCapsPatchLines(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	// A patch far longer than max_lines must come back cut and flagged, so a huge
	// diff cannot silently consume the agent's whole context window.
	var big strings.Builder
	for i := 0; i < 200; i++ {
		big.WriteString("line ")
		big.WriteString(strconv.Itoa(i))
		big.WriteString("\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte(big.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	m := envOf(t)(doDiff(opCtx(nil, map[string]string{"repo_path": dir, "max_lines": "10"})))
	if n := len(strings.Split(m["stdout"].(string), "\n")); n > 10 {
		t.Errorf("stdout has %d lines, want at most the requested 10", n)
	}
	if m["truncated"] != true {
		t.Error("a cut patch must be flagged as truncated")
	}
}

func TestReadOpsAreNotDestructive(t *testing.T) {
	// Read ops must not be marked destructive: the framework defaults destructive
	// ops to disabled, which would make inspecting a repo an admin opt-in.
	//
	// Scoped to the Read category. Before Task 9 this walked every category, which
	// was equivalent because only reads existed; asserting it globally now would
	// forbid the destructive ops that are required to be destructive.
	for _, cat := range Operations() {
		for _, op := range cat.Ops {
			if cat.Title == "Read" && op.Destructive {
				t.Errorf("read op %q is marked destructive", op.Key)
			}
			if strings.TrimSpace(op.Description) == "" {
				t.Errorf("op %q has no description; the LLM has nothing to select on", op.Key)
			}
			if len(op.Input) == 0 {
				t.Errorf("op %q reflected no input rows; its Input struct tags are wrong", op.Key)
			}
		}
	}
}

func TestReadOpKeysAreUniqueSlugs(t *testing.T) {
	slug := regexp.MustCompile(`^[a-z0-9_]+$`)
	seen := map[string]bool{}
	for _, cat := range Operations() {
		for _, op := range cat.Ops {
			if !slug.MatchString(op.Key) {
				t.Errorf("op key %q is not a lowercase slug", op.Key)
			}
			if seen[op.Key] {
				t.Errorf("op key %q is declared twice", op.Key)
			}
			seen[op.Key] = true
			if op.Execute == nil {
				t.Errorf("op %q has no Execute func", op.Key)
			}
		}
	}
	for _, want := range []string{"status", "log", "diff", "branch_list", "show", "remote_list", "ls_remote"} {
		if !seen[want] {
			t.Errorf("read op %q is missing from Operations()", want)
		}
	}
}

func TestInjectedArgsSuppressHooksByDefault(t *testing.T) {
	// A repository hook is arbitrary code that ships with the repo, so the default
	// must be suppression: core.hooksPath is redirected at an empty directory.
	got := strings.Join(injectedArgs(testCtx(map[string]string{}), AuthSpec{}, "commit"), " ")
	if !strings.Contains(got, "core.hooksPath=") {
		t.Errorf("injectedArgs(commit) = %q, want core.hooksPath suppression", got)
	}

	if got := strings.Join(injectedArgs(testCtx(map[string]string{"allow_hooks": "true"}),
		AuthSpec{}, "commit"), " "); strings.Contains(got, "core.hooksPath=") {
		t.Errorf("allow_hooks=true must let hooks run, got %q", got)
	}

	// A read op never triggers a hook, so there is no hooksPath to suppress — but it
	// still carries the credential.helper reset, because a read can reach the
	// network too (ls_remote) and the machine's credential manager must never be
	// consulted on any operation.
	got = strings.Join(injectedArgs(testCtx(map[string]string{}), AuthSpec{}, "status"), " ")
	if strings.Contains(got, "core.hooksPath=") {
		t.Errorf("injectedArgs(status) = %q, want no hook suppression on a read op", got)
	}
	if !strings.Contains(got, "credential.helper=") {
		t.Errorf("injectedArgs(status) = %q, want the credential.helper reset on every op", got)
	}
}

func TestInjectedArgsCarryIdentity(t *testing.T) {
	got := injectedArgs(testCtx(map[string]string{
		"author_name":  " Deploy Bot ",
		"author_email": "bot@example.com",
	}), AuthSpec{}, "status")
	joined := strings.Join(got, " ")

	if !strings.Contains(joined, "user.name=Deploy Bot") {
		t.Errorf("injectedArgs = %q, want the trimmed author name", joined)
	}
	if !strings.Contains(joined, "user.email=bot@example.com") {
		t.Errorf("injectedArgs = %q, want the author email", joined)
	}
	// Identity is injected via -c, which ValidateUserArgs bans. It must therefore
	// only ever travel in InjectedArgs — asserting that here keeps the two paths
	// from being merged by a later refactor.
	if err := ValidateUserArgs(got); err == nil {
		t.Error("injected -c args are expected to fail ValidateUserArgs; they must never be passed as UserArgs")
	}
}

func TestExecuteDeniedOpNeverSpawns(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)

	// A denial must be decided before any process is started, and before the
	// argument builder even runs: the envelope reports exit_code -1 ("never ran"),
	// which a caller must not confuse with git's own exit code.
	built := false
	m := envOf(t)(execute(testCtx(map[string]string{
		"protected_branches": `[{"branch":"main"}]`,
	}), "push", dir, Request{Branch: "main"},
		func(EffectivePolicy) ([]string, error) {
			built = true
			return []string{"push"}, nil
		}, false))

	if built {
		t.Error("the argument builder ran for a denied operation")
	}
	if m["ok"] != false || m["exit_code"] != -1 {
		t.Errorf("denied envelope = %+v, want ok=false and exit_code=-1", m)
	}
	if pol := m["policy"].(map[string]any); pol["verdict"] != "deny" || pol["reason"] == nil {
		t.Errorf("policy = %v, want a deny verdict with a reason", pol)
	}

	// The same call with nothing protected must reach the builder — otherwise the
	// test above would pass even if execute never built anything at all.
	built = false
	if _, err := execute(testCtx(map[string]string{}), "push", dir, Request{Branch: "main"},
		func(EffectivePolicy) ([]string, error) {
			built = true
			return []string{"status"}, nil
		}, false); err != nil {
		t.Fatalf("allowed operation returned an error: %v", err)
	}
	if !built {
		t.Error("an allowed operation must reach the argument builder")
	}
}

func TestCurrentBranchAndRemoteURL(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	c := testCtx(map[string]string{})

	if got := currentBranch(c, dir); got != "main" {
		t.Errorf("currentBranch = %q, want main", got)
	}
	// No remote configured: the lookup must return "" rather than an error, so the
	// repo slug is empty and only path-based policy rules can match.
	if got := remoteURL(c, dir, "origin"); got != "" {
		t.Errorf("remoteURL with no remote = %q, want empty", got)
	}
}

func TestConfigDescriptionsAreEnglishAndPresent(t *testing.T) {
	for _, cfg := range entity.StructToConfigs(Config{}) {
		if strings.TrimSpace(cfg.Description) == "" {
			t.Errorf("config %q has no desc; the admin UI would show a bare label", cfg.Key)
		}
	}
}

// ---------------------------------------------------------------------------
// Task 9: mutating and destructive operations
// ---------------------------------------------------------------------------

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
	for _, a := range args {
		if a == "--force" || a == "-f" {
			t.Errorf("bare --force must not be used, got: %s", joined)
		}
	}
}

func TestPushArgsGuardTheURLPositional(t *testing.T) {
	// The URL and refspec are positionals. A remote configured as
	// "--receive-pack=evil" would otherwise become a flag that runs an arbitrary
	// binary on the server side, which is exactly what the deny-list cannot see
	// once the value is a positional rather than a known flag.
	args := buildPushArgs("--receive-pack=evil", "main", false, false)
	idx := indexOf(args, "--end-of-options")
	if idx < 0 {
		t.Fatalf("push args have no --end-of-options guard: %v", args)
	}
	if indexOf(args, "--receive-pack=evil") < idx {
		t.Errorf("the URL sits before the guard, so the guard protects nothing: %v", args)
	}
}

// indexOf returns the position of want in args, or -1. Used to assert that a
// user value sits AFTER the --end-of-options terminator, which is the only
// placement that actually guards it.
func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

func TestMutatingOpsRoundTripAgainstTempRepo(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	// A real branch_create → add → commit → tag → stash round trip. An
	// argument-shape assertion cannot catch a guard that git rejects outright, or
	// an argument order git parses differently than intended; running the real
	// binary can.
	cfg := map[string]string{"author_name": "Deploy Bot", "author_email": "bot@example.com"}
	env := envOf(t)

	m := env(doBranchCreate(opCtx(cfg, map[string]string{
		"repo_path": dir, "name": "fix/login-timeout", "checkout": "true",
	})))
	if m["ok"] != true {
		t.Fatalf("branch_create failed: %+v", m)
	}
	if got := currentBranch(testCtx(cfg), dir); got != "fix/login-timeout" {
		t.Fatalf("currentBranch = %q, want the newly created branch", got)
	}

	if err := os.WriteFile(filepath.Join(dir, "app.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if m := env(doAdd(opCtx(cfg, map[string]string{
		"repo_path": dir, "paths": "app.txt",
	}))); m["ok"] != true {
		t.Fatalf("add failed: %+v", m)
	}

	m = env(doCommit(opCtx(cfg, map[string]string{
		"repo_path": dir, "message": "feat: add app",
	})))
	if m["ok"] != true {
		t.Fatalf("commit failed: %+v", m)
	}
	// The commit must carry the configured identity, which reaches git only
	// through injectedArgs.
	logOut := env(doLog(opCtx(cfg, map[string]string{"repo_path": dir, "limit": "1"})))["stdout"].(string)
	if !strings.Contains(logOut, "feat: add app") {
		t.Errorf("the commit is not in the log:\n%s", logOut)
	}
	if !strings.Contains(logOut, "Deploy Bot") {
		t.Errorf("the commit does not carry the configured author:\n%s", logOut)
	}

	if m := env(doTag(opCtx(cfg, map[string]string{
		"repo_path": dir, "name": "v1.2.0", "message": "release 1.2.0",
	}))); m["ok"] != true {
		t.Fatalf("annotated tag failed: %+v", m)
	}
	if m := env(doTagDelete(opCtx(cfg, map[string]string{
		"repo_path": dir, "name": "v1.2.0",
	}))); m["ok"] != true {
		t.Fatalf("tag_delete failed: %+v", m)
	}

	// stash push → list → drop, on a dirty working tree so there is something to
	// save.
	if err := os.WriteFile(filepath.Join(dir, "app.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if m := env(doStash(opCtx(cfg, map[string]string{
		"repo_path": dir, "action": "push", "message": "wip",
	}))); m["ok"] != true {
		t.Fatalf("stash push failed: %+v", m)
	}
	if m := env(doStash(opCtx(cfg, map[string]string{
		"repo_path": dir, "action": "list",
	}))); !strings.Contains(m["stdout"].(string), "wip") {
		t.Errorf("the stash label is missing from the list: %+v", m)
	}
	if m := env(doStashDrop(opCtx(cfg, map[string]string{
		"repo_path": dir,
	}))); m["ok"] != true {
		t.Fatalf("stash_drop failed: %+v", m)
	}

	// checkout back, then merge the feature branch: both hook-running ops, so this
	// also exercises the core.hooksPath suppression path.
	if m := env(doCheckout(opCtx(cfg, map[string]string{
		"repo_path": dir, "ref": "main",
	}))); m["ok"] != true {
		t.Fatalf("checkout failed: %+v", m)
	}
	if m := env(doMerge(opCtx(cfg, map[string]string{
		"repo_path": dir, "ref": "fix/login-timeout", "no_ff": "true",
	}))); m["ok"] != true {
		t.Fatalf("merge failed: %+v", m)
	}
	// reset --soft undoes the merge commit without touching the working tree, so
	// the repo is left usable and the soft path is covered.
	if m := env(doReset(opCtx(cfg, map[string]string{
		"repo_path": dir, "mode": "soft", "ref": "HEAD~1",
	}))); m["ok"] != true {
		t.Fatalf("soft reset failed: %+v", m)
	}
}

func TestMutatingOpsGuardPositionalUserValues(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	// Every user value in a positional slot must sit after --end-of-options, so a
	// flag-shaped value is data rather than an option. These run for real: the
	// assertion is that git refused the value AND that the guard is in the argv
	// ahead of it, which is what makes the refusal structural rather than lucky.
	cases := []struct {
		name  string
		h     connector.ExecuteFunc
		in    map[string]string
		value string
	}{
		{"branch_create name", doBranchCreate,
			map[string]string{"repo_path": dir, "name": "--delete"}, "--delete"},
		{"checkout ref", doCheckout,
			map[string]string{"repo_path": dir, "ref": "--orphan"}, "--orphan"},
		{"tag name", doTag,
			map[string]string{"repo_path": dir, "name": "-d"}, "-d"},
		{"merge ref", doMerge,
			map[string]string{"repo_path": dir, "ref": "--abort"}, "--abort"},
		// The worst case in the set: without the guard "reset --soft --hard"
		// silently performs a HARD reset and exits 0.
		{"reset ref", doReset,
			map[string]string{"repo_path": dir, "mode": "soft", "ref": "--hard"}, "--hard"},
		{"rebase onto", doRebase,
			map[string]string{"repo_path": dir, "onto": "--root"}, "--root"},
		{"stash_drop ref", doStashDrop,
			map[string]string{"repo_path": dir, "ref": "--all"}, "--all"},
		{"tag_delete name", doTagDelete,
			map[string]string{"repo_path": dir, "name": "--all"}, "--all"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Two independent defences, and either one is a pass.
			//
			// Some of these values are now refused by ValidateRefName BEFORE an argv exists
			// — a leading "-" is rejected as a value, wherever it would have landed. That is
			// the stronger defence, because it does not depend on the value ending up in the
			// position somebody remembered to guard: push embedded its branch inside
			// "HEAD:refs/heads/<branch>" and was protected by neither the deny-list nor a
			// terminator until the value itself was checked.
			//
			// The terminator still matters for values that are legitimately ref-shaped but
			// happen to collide with a flag name, so where the op DID build an argv, the
			// guard must still be in it and ahead of the value.
			out, err := tc.h(opCtx(nil, tc.in))
			if err != nil {
				if !strings.Contains(err.Error(), tc.value) {
					t.Errorf("%s was refused, but the error does not name the value %q: %v",
						tc.name, tc.value, err)
				}
				return
			}
			m := envOf(t)(out, nil)
			if m["ok"] == true {
				t.Fatalf("%s accepted a flag-shaped value: %+v", tc.name, m)
			}
			cmd := m["command"].(string)
			idx := strings.Index(cmd, "--end-of-options")
			if idx < 0 {
				t.Fatalf("%s argv has no --end-of-options guard: %s", tc.name, cmd)
			}
			// The value must sit AFTER the guard, otherwise the guard protects nothing.
			if !strings.Contains(cmd[idx:], tc.value) {
				t.Errorf("%s put %q before --end-of-options: %s", tc.name, tc.value, cmd)
			}
		})
	}
}

func TestResetHardRequiresForceOptIn(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	// A hard reset discards committed and uncommitted work, so it is mapped onto
	// Request{Force: true} and needs the same opt-in as a force push. Nothing runs.
	m := envOf(t)(doReset(opCtx(nil, map[string]string{
		"repo_path": dir, "mode": "hard", "ref": "HEAD~1",
	})))
	if m["ok"] != false || m["exit_code"] != -1 {
		t.Fatalf("hard reset was not blocked before spawning: %+v", m)
	}
	pol := m["policy"].(map[string]any)
	if pol["verdict"] != "deny" {
		t.Errorf("verdict = %v, want deny", pol["verdict"])
	}
	if r, _ := pol["reason"].(string); !strings.Contains(r, "force") {
		t.Errorf("reason = %q, want it to name the force-push opt-in", r)
	}

	// With the opt-in it is allowed. Reset to HEAD so the repo is unchanged.
	m = envOf(t)(doReset(opCtx(map[string]string{"allow_force_push": "true"},
		map[string]string{"repo_path": dir, "mode": "hard", "ref": "HEAD"})))
	if m["ok"] != true {
		t.Fatalf("hard reset with allow_force_push failed: %+v", m)
	}
}

func TestResetRejectsUnknownMode(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	// The mode is interpolated into "--<mode>", so it must come from a closed set:
	// a crafted mode must never reach git as a flag.
	if _, err := doReset(opCtx(nil, map[string]string{
		"repo_path": dir, "mode": "hard --exec-path=/tmp", "ref": "HEAD",
	})); err == nil {
		t.Error("an unknown reset mode must be rejected before git runs")
	}
}

func TestCommitOnProtectedBranchIsBlocked(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	// initTestRepo checks out main, which is protected here, so the commit must be
	// refused on the current branch rather than on a named target.
	m := envOf(t)(doCommit(opCtx(map[string]string{
		"protected_branches": `[{"branch":"main"}]`,
	}, map[string]string{"repo_path": dir, "message": "sneaky"})))

	if m["ok"] != false || m["exit_code"] != -1 {
		t.Fatalf("commit on a protected branch was not blocked: %+v", m)
	}
	if r, _ := m["policy"].(map[string]any)["reason"].(string); !strings.Contains(r, "protected") {
		t.Errorf("reason = %q, want it to name the protected branch", r)
	}
}

func TestBranchCreateEnforcesNamePattern(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	cfg := map[string]string{"branch_name_pattern": `^(fix|feat)/[a-z0-9._-]+$`}

	m := envOf(t)(doBranchCreate(opCtx(cfg, map[string]string{
		"repo_path": dir, "name": "random-branch",
	})))
	if m["ok"] != false || m["exit_code"] != -1 {
		t.Fatalf("a branch violating the pattern was not blocked: %+v", m)
	}
	if r, _ := m["policy"].(map[string]any)["reason"].(string); !strings.Contains(r, "pattern") {
		t.Errorf("reason = %q, want it to name the pattern", r)
	}

	if m := envOf(t)(doBranchCreate(opCtx(cfg, map[string]string{
		"repo_path": dir, "name": "feat/ok",
	}))); m["ok"] != true {
		t.Fatalf("a conforming branch name was rejected: %+v", m)
	}
}

func TestDryRunAssemblesWithoutExecuting(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)

	t.Run("commit", func(t *testing.T) {
		logOf := func() string {
			return envOf(t)(doLog(opCtx(nil, map[string]string{
				"repo_path": dir, "limit": "20",
			})))["stdout"].(string)
		}
		before := logOf()
		m := envOf(t)(doCommit(opCtx(nil, map[string]string{
			"repo_path": dir, "message": "not really", "dry_run": "true",
		})))
		if m["ok"] != true {
			t.Fatalf("dry run should report the allow verdict: %+v", m)
		}
		if !strings.Contains(m["command"].(string), "not really") {
			t.Errorf("dry run must report the assembled command, got %q", m["command"])
		}
		if before != logOf() {
			t.Error("a dry-run commit changed the repository")
		}
	})

	t.Run("commit denied still does not execute", func(t *testing.T) {
		m := envOf(t)(doCommit(opCtx(map[string]string{
			"protected_branches": `[{"branch":"main"}]`,
		}, map[string]string{"repo_path": dir, "message": "x", "dry_run": "true"})))
		if m["ok"] != false {
			t.Errorf("a dry run on a protected branch must report the denial: %+v", m)
		}
	})

	t.Run("raw", func(t *testing.T) {
		// raw's dry run must still honour the allow list: the verdict comes first.
		m := envOf(t)(doRaw(opCtx(map[string]string{
			"raw_enabled": "true", "raw_rules": `[{"subcommand":"bisect","mode":"allow"}]`,
		}, map[string]string{"repo_path": dir, "args": "bisect start", "dry_run": "true"})))
		if m["ok"] != true {
			t.Fatalf("an allow-listed raw dry run failed: %+v", m)
		}
		if !strings.Contains(m["command"].(string), "bisect start") {
			t.Errorf("raw dry run must report the command, got %q", m["command"])
		}
	})
}

func TestDryRunMasksTheCredential(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	// A dry run reports a command string. It must be masked like a real one,
	// otherwise dry_run becomes a way to read back the configured token.
	const token = "ghp_supersecrettoken12345"
	m := envOf(t)(doRaw(opCtx(map[string]string{
		"raw_enabled": "true", "raw_rules": `[{"subcommand":"log","mode":"allow"}]`,
		"token": token, "username": "x-access-token",
	}, map[string]string{
		"repo_path": dir, "args": "log --format=" + token, "dry_run": "true",
	})))
	if strings.Contains(m["command"].(string), token) {
		t.Errorf("the dry-run command leaked the token: %q", m["command"])
	}
}

func TestRawFailsClosed(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)

	cases := []struct {
		name string
		cfg  map[string]string
		args string
	}{
		{"disabled by default", map[string]string{}, "status"},
		{"subcommand not listed",
			map[string]string{"raw_enabled": "true"}, "push"},
		{"subcommand explicitly denied",
			map[string]string{"raw_enabled": "true", "raw_rules": `[{"subcommand":"push","mode":"deny"}]`}, "push"},
		// RawSubcommandOf cannot name a subcommand here, so the policy sees "" and
		// denies. Guessing "5" from "-n 5" would be worse than refusing.
		{"unrecognised leading flag",
			map[string]string{"raw_enabled": "true", "raw_rules": `[{"subcommand":"log","mode":"allow"}]`}, "-n 5 log"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := envOf(t)(doRaw(opCtx(tc.cfg, map[string]string{
				"repo_path": dir, "args": tc.args,
			})))
			if m["ok"] != false || m["exit_code"] != -1 {
				t.Fatalf("raw %q was not denied: %+v", tc.args, m)
			}
		})
	}
}

func TestRawStillAppliesTheArgumentDenyList(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	// An allow-listed subcommand does not exempt the arguments. "-c" sets arbitrary
	// config (core.pager, alias.x=!cmd), which is shell execution, so it must be
	// refused even for a subcommand the admin allowed.
	if _, err := doRaw(opCtx(map[string]string{
		"raw_enabled": "true", "raw_rules": `[{"subcommand":"log","mode":"allow"}]`,
	}, map[string]string{
		"repo_path": dir, "args": "-c core.pager=sh log",
	})); err == nil {
		t.Error("raw must reject -c even when the subcommand is allow-listed")
	}
}

func TestCloneRefusesExistingDest(t *testing.T) {
	// An existing destination is refused before any network call: cloning into a
	// populated directory would either fail obscurely or leave it ambiguous which
	// files came from the clone.
	dir := t.TempDir()
	if _, err := doClone(opCtx(nil, map[string]string{
		"url": "https://abc.com/org/repo.git", "dest": dir,
	})); err == nil {
		t.Error("clone into an existing dest must be refused")
	}
	if _, err := doClone(opCtx(nil, map[string]string{
		"url": "https://abc.com/org/repo.git", "dest": "",
	})); err == nil {
		t.Error("clone without a dest must be refused")
	}
}

func TestCloneRejectsUnconvertibleSSHRemote(t *testing.T) {
	// An ~/.ssh/config host alias cannot be converted to HTTPS without guessing a
	// hostname, so clone must fail loudly rather than reach the wrong server.
	if _, err := doClone(opCtx(map[string]string{"convert_ssh_remote_to_https": "true"},
		map[string]string{
			"url":  "myserver:org/repo.git",
			"dest": filepath.Join(t.TempDir(), "fresh"),
		})); err == nil {
		t.Error("an SSH host alias must be rejected rather than mechanically converted")
	}
}

func TestPushRequiresABranchWhenHeadIsDetached(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	// Detach HEAD, then push without an explicit branch. There is no current
	// branch to infer, and inventing one could publish to the wrong ref.
	runInRepo(t, dir, "checkout", "--detach", "HEAD")
	if _, err := doPush(opCtx(nil, map[string]string{"repo_path": dir})); err == nil {
		t.Error("push on a detached HEAD with no branch must be refused")
	}
}

func TestPushToProtectedBranchIsBlockedBeforeSpawn(t *testing.T) {
	// Evaluate directly: the policy must deny without any process running, so this
	// test needs no git and no network.
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

	env := deniedEnvelope(v, "git push origin main", "push").(map[string]any)
	if env["ok"] != false {
		t.Error("a denied envelope must report ok=false")
	}
	if env["exit_code"] != -1 {
		t.Errorf("exit_code = %v, want -1 to mark that nothing ran", env["exit_code"])
	}
}

func TestPushDryRunNeverTouchesTheNetwork(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	// A remote pointing at a host that cannot resolve: if the dry run reached the
	// network this would fail instead of reporting an allow verdict.
	runInRepo(t, dir, "remote", "add", "origin", "https://invalid.invalid/org/repo.git")

	m := envOf(t)(doPush(opCtx(nil, map[string]string{
		"repo_path": dir, "dry_run": "true",
	})))
	if m["ok"] != true {
		t.Fatalf("push dry run failed: %+v", m)
	}
	cmdStr := m["command"].(string)
	// The remote NAME, not its URL. This used to assert the opposite, for a real
	// reason — a credential embedded in .git/config wins over GIT_ASKPASS, so passing
	// the stripped URL kept the connector from authenticating as whoever's password is
	// in the file. But the URL also cost upstream tracking: --set-upstream recorded the
	// URL string as branch.<b>.remote, so status lost ahead/behind for every branch the
	// connector created. The credential is now neutralised by a
	// url.<clean>.insteadOf=<dirty> injection instead, which keeps both properties.
	if !strings.Contains(cmdStr, "--end-of-options origin ") {
		t.Errorf("push must name the remote, or git cannot record upstream; got %q", cmdStr)
	}
	if strings.Contains(cmdStr, "https://invalid.invalid") {
		t.Errorf("push must not pass the URL as the remote argument, got %q", cmdStr)
	}
	if !strings.Contains(cmdStr, "HEAD:refs/heads/main") {
		t.Errorf("dry run must report a full refspec, got %q", cmdStr)
	}
	if rem, ok := m["remote"].(map[string]any); !ok || rem["effective"] == nil {
		t.Errorf("a push response must report the effective remote, got %+v", m["remote"])
	}
}

func TestPushForceRequiresPolicyOptIn(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	runInRepo(t, dir, "remote", "add", "origin", "https://invalid.invalid/org/repo.git")

	m := envOf(t)(doPush(opCtx(nil, map[string]string{
		"repo_path": dir, "force": "true", "dry_run": "true",
	})))
	if m["ok"] != false || m["exit_code"] != -1 {
		t.Fatalf("a force push without the opt-in must be denied: %+v", m)
	}

	// With the opt-in the assembled command must use the lease form.
	m = envOf(t)(doPush(opCtx(map[string]string{"allow_force_push": "true"},
		map[string]string{"repo_path": dir, "force": "true", "dry_run": "true"})))
	if m["ok"] != true {
		t.Fatalf("a force push with the opt-in was denied: %+v", m)
	}
	if !strings.Contains(m["command"].(string), "--force-with-lease") {
		t.Errorf("force push must use --force-with-lease, got %q", m["command"])
	}
}

func TestNetworkOpsRejectAnUnresolvableRemote(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	// A repo with no remote at all: remoteURL returns "", so ConvertRemote fails
	// with "remote URL is empty" rather than the op silently doing nothing.
	for name, h := range map[string]connector.ExecuteFunc{
		"fetch": doFetch, "pull": doPull, "push": doPush,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := h(opCtx(nil, map[string]string{"repo_path": dir})); err == nil {
				t.Error("an operation against a nonexistent remote must be refused")
			}
		})
	}
}

func TestTagDeleteOnRemoteIsANetworkMutation(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	// With a remote set, tag_delete becomes "push :refs/tags/<name>" against the
	// explicit URL. A missing name must be refused before that is assembled.
	if _, err := doTagDelete(opCtx(nil, map[string]string{
		"repo_path": dir, "name": "", "remote": "origin",
	})); err == nil {
		t.Error("tag_delete with no name must be refused")
	}
	// No remote configured, so the URL lookup fails rather than guessing a host.
	if _, err := doTagDelete(opCtx(nil, map[string]string{
		"repo_path": dir, "name": "v1.0.0", "remote": "origin",
	})); err == nil {
		t.Error("tag_delete against a nonexistent remote must be refused")
	}
}

func TestMutatingOpsRejectMissingRepoPath(t *testing.T) {
	// Every mutating op validates the repo path before anything else, so a bad path
	// is a Go error rather than a git failure buried in stderr.
	handlers := map[string]connector.ExecuteFunc{
		"branch_create": doBranchCreate, "checkout": doCheckout, "add": doAdd,
		"commit": doCommit, "stash": doStash, "tag": doTag,
		"fetch": doFetch, "pull": doPull, "push": doPush, "merge": doMerge,
		"reset": doReset, "rebase": doRebase,
		"stash_drop": doStashDrop, "tag_delete": doTagDelete, "raw": doRaw,
	}
	in := map[string]string{
		"name": "feat/x", "ref": "HEAD", "paths": ".", "message": "m",
		"action": "list", "mode": "soft", "onto": "main", "args": "status",
	}
	for name, h := range handlers {
		t.Run(name, func(t *testing.T) {
			for _, repo := range []string{"", filepath.Join(t.TempDir(), "nope")} {
				input := map[string]string{"repo_path": repo}
				for k, v := range in {
					input[k] = v
				}
				if _, err := h(opCtx(nil, input)); err == nil {
					t.Errorf("repo_path %q must be rejected", repo)
				}
			}
		})
	}
}

func TestDestructiveOpsAreDeclaredDestructive(t *testing.T) {
	// The framework defaults destructive ops to disabled per instance, so this
	// declaration is the opt-in gate. stash_drop and tag_delete are separate ops
	// precisely because destructiveness belongs to the operation, not to an
	// argument — folding them back into stash/tag as flags would let them run
	// under a non-destructive op's permission.
	want := map[string]bool{
		"push": true, "merge": true, "reset": true, "rebase": true, "clone": true,
		"stash_drop": true, "tag_delete": true, "raw": true,
	}
	got := map[string]bool{}
	for _, cat := range Operations() {
		for _, op := range cat.Ops {
			if op.Destructive {
				got[op.Key] = true
			}
		}
	}
	for key := range want {
		if !got[key] {
			t.Errorf("op %q must be declared with connector.OpDestructive", key)
		}
	}
	for key := range got {
		if !want[key] {
			t.Errorf("op %q is marked destructive unexpectedly", key)
		}
	}
}

func TestEveryDeclaredOpHasAHandlerAndUniqueKey(t *testing.T) {
	slug := regexp.MustCompile(`^[a-z0-9_]+$`)
	seen := map[string]bool{}
	for _, cat := range Operations() {
		if strings.TrimSpace(cat.Title) == "" {
			t.Error("a category has no title")
		}
		for _, op := range cat.Ops {
			if !slug.MatchString(op.Key) {
				t.Errorf("op key %q is not a lowercase slug", op.Key)
			}
			if seen[op.Key] {
				t.Errorf("op key %q is declared twice", op.Key)
			}
			seen[op.Key] = true
			if op.Execute == nil {
				t.Errorf("op %q has no Execute func", op.Key)
			}
		}
	}
	for _, want := range []string{
		"status", "log", "diff", "branch_list", "show", "remote_list", "ls_remote",
		"branch_create", "checkout", "add", "commit", "stash", "tag",
		"fetch", "pull",
		"push", "merge", "reset", "rebase", "clone", "stash_drop", "tag_delete", "raw",
	} {
		if !seen[want] {
			t.Errorf("op %q is missing from Operations()", want)
		}
	}
}

func TestMutatingOpsAreCoveredByThePolicyEngine(t *testing.T) {
	// Every op that changes local repository state or publishes to a remote must be
	// in mutatingOps, otherwise Evaluate skips the branch, protection and force
	// checks entirely and the op runs unguarded.
	//
	// fetch is the one deliberate exception: it only updates remote-tracking refs,
	// leaving the working tree, the index and every local branch alone. There is no
	// target branch for the protection check to judge, so listing it would only
	// mean a malformed policy blocked an operation that cannot damage anything.
	exempt := map[string]bool{"fetch": true}

	for _, cat := range Operations() {
		if cat.Title == "Read" {
			continue
		}
		for _, op := range cat.Ops {
			if exempt[op.Key] {
				continue
			}
			// Config-only ops back the settings form: they render markup and
			// validate text, never spawn git and never name a repository, so
			// there is nothing for the branch/protection/force checks to judge.
			// They are excluded structurally rather than by name, so a new widget
			// op cannot be waved through by adding it to a list.
			if op.ConfigOnly {
				continue
			}
			if !mutatingOps[op.Key] {
				t.Errorf("op %q mutates but is not in mutatingOps, so the policy skips its checks", op.Key)
			}
		}
	}
	// Guard the exemption itself: if fetch is ever added to mutatingOps, the
	// carve-out above is stale and should be deleted rather than left lying.
	for key := range exempt {
		if mutatingOps[key] {
			t.Errorf("op %q is now in mutatingOps; remove it from this test's exempt list", key)
		}
	}
}

// runInRepo runs git directly in a test repo, bypassing the connector, to set up
// a precondition an operation cannot create itself (a detached HEAD, a remote
// pointing at an unresolvable host). safeexec, not os/exec, per the package rule.
func runInRepo(t *testing.T, dir string, args ...string) {
	t.Helper()
	gitPath, err := ResolveGit()
	if err != nil {
		t.Skip("git not installed")
	}
	cmd := safeexec.Command(gitPath, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
