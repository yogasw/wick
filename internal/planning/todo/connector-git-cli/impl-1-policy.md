# Git CLI Connector — Implementation Plan (Part 1 of 3: Policy Engine)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the policy engine and remote-URL layer for a wick connector plugin that wraps the local `git` CLI, blocking unsafe operations before any process spawns.

**Architecture:** Out-of-tree plugin at `plugins/connector/git/`, following the `pkg/connector` contract exactly as `plugins/connector/loki/` does. Two config layers (global fields + per-repo rows) compile into one `EffectivePolicy` before evaluation; no operation reads raw config. Part 1 builds the pure-Go core with zero I/O — it is fully unit-testable without git installed.

**Tech Stack:** Go, `github.com/yogasw/wick/pkg/connector`, `github.com/yogasw/wick/pkg/entity`, `github.com/yogasw/wick/pkg/wickdocs`, `github.com/yogasw/wick/plugins/tags`. Stdlib only otherwise (`regexp`, `path`, `net/url`, `encoding/json`).

**Spec:** [plan.md](plan.md) — read §2, §3, §5 before starting.

## Global Constraints

- Plugin folder name MUST equal `Meta.Key` = `git`. The build fails otherwise.
- Op keys: `a-z0-9_` only. No hyphens, no spaces.
- Package is `package main` (plugin binary), not a library package.
- All user-facing config text (`desc=`, group titles, widget copy) in **English**.
- Sample values in docs/godoc use generic names (`abc.com`, `example.com`, `org/repo`) — never "qiscus".
- Never write `Date.now()`-style nondeterminism into tests; table tests only.
- `regexp` = Go RE2. No lookahead/backreference — reject patterns that fail `regexp.Compile`.
- Empty policy column = **inherit**. `"-"` = **clear inherited value**. Never "allow anything".
- Every deny verdict MUST carry a non-empty `MatchedRule` string.
- Commit after each task. Conventional Commits format, no AI attribution trailers.

## File Structure (all 3 parts)

| File | Responsibility | Part |
|---|---|---|
| `policy.go` | `EffectivePolicy`, layer resolution, `Evaluate` | 1 |
| `policy_test.go` | table tests for resolution + evaluation | 1 |
| `remote.go` | parse remotes, strip credentials, SSH→HTTPS, host map | 1 |
| `remote_test.go` | conversion table + must-fail cases | 1 |
| `git.go` | argv builder, env allowlist, timeout, output cap, runner | 2 |
| `git_test.go` | deny-list, env leak, truncation, process-group kill | 2 |
| `service.go` | per-op input validation + argument assembly | 2 |
| `connector.go` | Meta, Config, Input structs, `Operations()` | 2 |
| `main.go` | `wickplugin.Serve(Module())` + `--askpass` mode | 2 |
| `policyui.go` | Policy Manager widget: render + mutate ops | 3 |
| `policyui_test.go` | widget render + field round-trip tests | 3 |
| `VERSION` | `0.1.0` | 2 |

---

### Task 1: Policy types and layer resolution

**Files:**
- Create: `plugins/connector/git/policy.go`
- Test: `plugins/connector/git/policy_test.go`

**Interfaces:**
- Consumes: nothing (first task).
- Produces:
  - `type RepoRule struct { Repo, BranchPattern, Protected, ForcePush string }`
  - `type EffectivePolicy struct { BranchPattern string; BranchRe *regexp.Regexp; Protected []string; AllowForcePush bool; RawEnabled bool; RawRules map[string]string; MatchedRule string; PolicyErr string }`
  - `func ParseRepoRules(jsonStr string) ([]RepoRule, error)`
  - `func ParseKVList(jsonStr string) ([]map[string]string, error)`
  - `func Specificity(glob string) int`
  - `func MatchRepo(glob, repoPath, repoSlug string) bool`
  - `func Resolve(g GlobalPolicy, rules []RepoRule, repoPath, repoSlug string) EffectivePolicy`
  - `type GlobalPolicy struct { BranchPattern string; Protected []string; AllowForcePush bool; RawEnabled bool; RawRules map[string]string }`

- [ ] **Step 1: Write the failing test for `ParseKVList` and `Specificity`**

```go
package main

import "testing"

func TestParseKVList(t *testing.T) {
	rows, err := ParseKVList(`[{"branch":"master"},{"branch":"release/*"}]`)
	if err != nil {
		t.Fatalf("ParseKVList: %v", err)
	}
	if len(rows) != 2 || rows[0]["branch"] != "master" || rows[1]["branch"] != "release/*" {
		t.Fatalf("unexpected rows: %v", rows)
	}
}

func TestParseKVListEmpty(t *testing.T) {
	for _, in := range []string{"", "   ", "[]", "null"} {
		rows, err := ParseKVList(in)
		if err != nil {
			t.Fatalf("ParseKVList(%q): unexpected error %v", in, err)
		}
		if len(rows) != 0 {
			t.Fatalf("ParseKVList(%q) = %v, want empty", in, rows)
		}
	}
}

func TestParseKVListInvalid(t *testing.T) {
	if _, err := ParseKVList(`{"branch":"master"}`); err == nil {
		t.Fatal("expected error for object instead of array")
	}
}

func TestSpecificity(t *testing.T) {
	cases := map[string]int{
		"*/org/sandbox":  1,
		"*/org/*":        2,
		"github.com/org/repo": 0,
		"*":              1,
		"d:/code/work/*": 1,
	}
	for glob, want := range cases {
		if got := Specificity(glob); got != want {
			t.Errorf("Specificity(%q) = %d, want %d", glob, got, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugins/connector/git/ -run 'TestParseKVList|TestSpecificity' -v`
Expected: FAIL — `undefined: ParseKVList`, `undefined: Specificity`

- [ ] **Step 3: Write minimal implementation**

```go
// Package main implements the git CLI connector plugin.
//
// policy.go holds the policy engine: it turns the two config layers (global
// fields + per-repo rows) into a single EffectivePolicy, then evaluates an
// operation against it. Everything here is pure Go — no I/O, no git — so the
// whole engine is unit-testable without git installed.
package main

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// ParseKVList decodes a kvlist config value. The manager stores kvlist fields as
// a JSON array of string-keyed objects; an unset field is the empty string.
func ParseKVList(s string) ([]map[string]string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		return nil, nil
	}
	var rows []map[string]string
	if err := json.Unmarshal([]byte(s), &rows); err != nil {
		return nil, fmt.Errorf("parse kvlist: %w", err)
	}
	return rows, nil
}

// Specificity counts wildcards in a repo glob. Fewer wildcards = more specific,
// so "*/org/sandbox" (1) beats "*/org/*" (2). Used to order competing rules
// deterministically instead of relying on the order rows were written.
func Specificity(glob string) int {
	return strings.Count(glob, "*")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugins/connector/git/ -run 'TestParseKVList|TestSpecificity' -v`
Expected: PASS

- [ ] **Step 5: Write the failing test for `MatchRepo`**

```go
func TestMatchRepo(t *testing.T) {
	cases := []struct {
		glob, repoPath, repoSlug string
		want                     bool
		note                     string
	}{
		{"*/org/infra", "d:/code/infra", "github.com/org/infra", true, "slug match"},
		{"*/org/infra", "d:/code/other", "github.com/org/other", false, "slug mismatch"},
		{"d:/code/work/*", "d:/code/work/api", "github.com/org/api", true, "path match"},
		{"d:/code/work/*", "d:/code/home/api", "github.com/org/api", false, "path mismatch"},
		{"*", "any", "any/any/any", true, "catch-all"},
		{"github.com/org/repo", "d:/x", "github.com/org/repo", true, "exact slug, no wildcard"},
		// Backslashes in Windows paths must be normalised before matching,
		// otherwise a d:/code/* glob never matches d:\code\work.
		{"d:/code/*", `d:\code\work`, "", true, "windows separators normalised"},
		// Case-insensitive on the path side: Windows paths vary in drive case.
		{"D:/Code/*", "d:/code/work", "", true, "case-insensitive path"},
	}
	for _, c := range cases {
		if got := MatchRepo(c.glob, c.repoPath, c.repoSlug); got != c.want {
			t.Errorf("MatchRepo(%q, %q, %q) = %v, want %v (%s)",
				c.glob, c.repoPath, c.repoSlug, got, c.want, c.note)
		}
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./plugins/connector/git/ -run TestMatchRepo -v`
Expected: FAIL — `undefined: MatchRepo`

- [ ] **Step 7: Implement `MatchRepo`**

```go
// MatchRepo reports whether a repo glob matches either the local path or the
// host/owner/repo slug. Matching both means one rule can be written either way:
// "*/org/infra" targets a remote, "d:/code/work/*" targets a checkout location.
//
// Separators are normalised to "/" and both sides lowercased so Windows paths
// (d:\code\Work) match a d:/code/* glob written by hand.
func MatchRepo(glob, repoPath, repoSlug string) bool {
	if glob == "" {
		return false
	}
	g := strings.ToLower(norm(glob))
	for _, candidate := range []string{repoPath, repoSlug} {
		if candidate == "" {
			continue
		}
		if ok, err := path.Match(g, strings.ToLower(norm(candidate))); err == nil && ok {
			return true
		}
	}
	return false
}

// norm converts Windows separators to forward slashes so one glob syntax works
// on every platform.
func norm(s string) string { return strings.ReplaceAll(s, `\`, "/") }
```

- [ ] **Step 8: Run test to verify it passes**

Run: `go test ./plugins/connector/git/ -run TestMatchRepo -v`
Expected: PASS

- [ ] **Step 9: Write the failing test for `Resolve`**

```go
func TestResolveInheritAndClear(t *testing.T) {
	global := GlobalPolicy{
		BranchPattern:  `^(fix|feat)/.+$`,
		Protected:      []string{"master", "main"},
		AllowForcePush: false,
	}
	rules := []RepoRule{
		// Empty columns inherit from global.
		{Repo: "*/org/api", BranchPattern: "", Protected: "", ForcePush: ""},
		// "-" clears the inherited protected list.
		{Repo: "*/org/sandbox", BranchPattern: ".*", Protected: "-", ForcePush: "true"},
		// More specific than */org/* below.
		{Repo: "*/org/infra", BranchPattern: `^ops/.+$`, Protected: "master,main,release/*", ForcePush: "false"},
		{Repo: "*/org/*", BranchPattern: `^team/.+$`, Protected: "master", ForcePush: "false"},
	}

	t.Run("empty columns inherit global", func(t *testing.T) {
		p := Resolve(global, rules, "d:/code/api", "github.com/org/api")
		if p.BranchPattern != `^(fix|feat)/.+$` {
			t.Errorf("BranchPattern = %q, want inherited global", p.BranchPattern)
		}
		if len(p.Protected) != 2 {
			t.Errorf("Protected = %v, want inherited 2 entries", p.Protected)
		}
		if p.MatchedRule == "" {
			t.Error("MatchedRule must never be empty")
		}
	})

	t.Run("dash clears inheritance", func(t *testing.T) {
		p := Resolve(global, rules, "d:/code/sandbox", "github.com/org/sandbox")
		if len(p.Protected) != 0 {
			t.Errorf("Protected = %v, want cleared by \"-\"", p.Protected)
		}
		if !p.AllowForcePush {
			t.Error("AllowForcePush = false, want true from rule")
		}
	})

	t.Run("most specific rule wins regardless of order", func(t *testing.T) {
		p := Resolve(global, rules, "d:/code/infra", "github.com/org/infra")
		if p.BranchPattern != `^ops/.+$` {
			t.Errorf("BranchPattern = %q, want ^ops/.+$ from the 1-wildcard rule", p.BranchPattern)
		}
		if len(p.Protected) != 3 {
			t.Errorf("Protected = %v, want 3 entries from the specific rule", p.Protected)
		}
	})

	t.Run("no matching rule falls back to global", func(t *testing.T) {
		p := Resolve(global, rules, "d:/code/solo", "gitlab.com/other/solo")
		if p.BranchPattern != `^(fix|feat)/.+$` {
			t.Errorf("BranchPattern = %q, want global", p.BranchPattern)
		}
		if p.MatchedRule != "global" {
			t.Errorf("MatchedRule = %q, want \"global\"", p.MatchedRule)
		}
	})
}

func TestResolveTieBreakPrefersStricter(t *testing.T) {
	global := GlobalPolicy{Protected: []string{"master"}}
	// Both rules have 1 wildcard — the tie must go to the stricter row (force
	// push denied), not to whichever was written first.
	rules := []RepoRule{
		{Repo: "*/org/api", ForcePush: "true"},
		{Repo: "github.com/*/api", ForcePush: "false"},
	}
	p := Resolve(global, rules, "d:/code/api", "github.com/org/api")
	if p.AllowForcePush {
		t.Error("AllowForcePush = true, want false — tie must prefer the stricter rule")
	}

	// Reversing the input order must not change the outcome.
	rev := []RepoRule{rules[1], rules[0]}
	if got := Resolve(global, rev, "d:/code/api", "github.com/org/api"); got.AllowForcePush {
		t.Error("result depends on rule order — it must not")
	}
}

func TestResolveInvalidRegexFailsClosed(t *testing.T) {
	global := GlobalPolicy{BranchPattern: `^(fix/.+$`} // missing closing paren
	p := Resolve(global, nil, "d:/code/api", "github.com/org/api")
	if p.PolicyErr == "" {
		t.Fatal("PolicyErr empty, want a compile error recorded")
	}
	if p.BranchRe != nil {
		t.Error("BranchRe must be nil when the pattern does not compile")
	}
}
```

- [ ] **Step 10: Run test to verify it fails**

Run: `go test ./plugins/connector/git/ -run TestResolve -v`
Expected: FAIL — `undefined: GlobalPolicy`, `undefined: RepoRule`, `undefined: Resolve`

- [ ] **Step 11: Implement the policy types and `Resolve`**

```go
// GlobalPolicy is layer 1 — the fallback used when no per-repo rule matches.
type GlobalPolicy struct {
	BranchPattern  string
	Protected      []string
	AllowForcePush bool
	RawEnabled     bool
	RawRules       map[string]string // subcommand → "allow" | "deny"
}

// RepoRule is one layer-2 row from the repo_policies kvlist. An empty column
// inherits from GlobalPolicy; "-" clears the inherited value.
type RepoRule struct {
	Repo          string
	BranchPattern string
	Protected     string // comma-separated globs, or "-" to clear
	ForcePush     string // "true" | "false" | "" (inherit) | "-" (inherit)
}

// EffectivePolicy is the single compiled policy every operation is judged
// against. Nothing downstream reads raw config — it reads this.
type EffectivePolicy struct {
	BranchPattern  string
	BranchRe       *regexp.Regexp // nil when BranchPattern is empty or invalid
	Protected      []string
	AllowForcePush bool
	RawEnabled     bool
	RawRules       map[string]string
	MatchedRule    string // "global" or "per-repo → <glob>"; never empty
	PolicyErr      string // non-empty when config is malformed → deny mutations
}

// ParseRepoRules decodes the repo_policies kvlist into typed rules.
func ParseRepoRules(s string) ([]RepoRule, error) {
	rows, err := ParseKVList(s)
	if err != nil {
		return nil, err
	}
	out := make([]RepoRule, 0, len(rows))
	for _, r := range rows {
		rule := RepoRule{
			Repo:          strings.TrimSpace(r["repo"]),
			BranchPattern: strings.TrimSpace(r["branch_pattern"]),
			Protected:     strings.TrimSpace(r["protected"]),
			ForcePush:     strings.TrimSpace(r["force_push"]),
		}
		if rule.Repo == "" {
			continue // a row with no repo glob can never match; drop it
		}
		out = append(out, rule)
	}
	return out, nil
}

// Resolve compiles the two layers into one EffectivePolicy for a specific repo.
//
// Layer 2 wins over layer 1, but only for the single best-matching rule: the
// fewest wildcards wins, and a tie goes to the stricter rule so the result never
// depends on the order rows were written.
func Resolve(g GlobalPolicy, rules []RepoRule, repoPath, repoSlug string) EffectivePolicy {
	p := EffectivePolicy{
		BranchPattern:  g.BranchPattern,
		Protected:      append([]string(nil), g.Protected...),
		AllowForcePush: g.AllowForcePush,
		RawEnabled:     g.RawEnabled,
		RawRules:       g.RawRules,
		MatchedRule:    "global",
	}

	if best, ok := bestRule(rules, repoPath, repoSlug); ok {
		p.MatchedRule = "per-repo → " + best.Repo
		if best.BranchPattern != "" {
			if best.BranchPattern == "-" {
				p.BranchPattern = ""
			} else {
				p.BranchPattern = best.BranchPattern
			}
		}
		switch best.Protected {
		case "": // inherit
		case "-":
			p.Protected = nil
		default:
			p.Protected = splitCSV(best.Protected)
		}
		switch best.ForcePush {
		case "true":
			p.AllowForcePush = true
		case "false":
			p.AllowForcePush = false
		} // "" and "-" inherit
	}

	if p.BranchPattern != "" {
		re, err := regexp.Compile(p.BranchPattern)
		if err != nil {
			p.PolicyErr = "branch pattern does not compile: " + err.Error()
		} else {
			p.BranchRe = re
		}
	}
	return p
}

// bestRule picks the winning layer-2 rule: fewest wildcards, then stricter.
func bestRule(rules []RepoRule, repoPath, repoSlug string) (RepoRule, bool) {
	var best RepoRule
	found := false
	for _, r := range rules {
		if !MatchRepo(r.Repo, repoPath, repoSlug) {
			continue
		}
		if !found {
			best, found = r, true
			continue
		}
		switch {
		case Specificity(r.Repo) < Specificity(best.Repo):
			best = r
		case Specificity(r.Repo) == Specificity(best.Repo) && stricter(r, best):
			best = r
		}
	}
	return best, found
}

// stricter reports whether a is more restrictive than b. Used only to break a
// specificity tie deterministically; denying force push and protecting more
// branches both count as stricter.
func stricter(a, b RepoRule) bool {
	if a.ForcePush != b.ForcePush {
		return a.ForcePush == "false"
	}
	if a.Protected == "-" || b.Protected == "-" {
		return b.Protected == "-"
	}
	return len(splitCSV(a.Protected)) > len(splitCSV(b.Protected))
}

func splitCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
```

- [ ] **Step 12: Run the full policy test file**

Run: `go test ./plugins/connector/git/ -run TestResolve -v`
Expected: PASS (all four subtests + tie-break + invalid-regex)

- [ ] **Step 13: Commit**

```bash
git add plugins/connector/git/policy.go plugins/connector/git/policy_test.go
git commit -m "feat(git): policy layer resolution with inherit and clear semantics"
```

---

### Task 2: Policy evaluation

**Files:**
- Modify: `plugins/connector/git/policy.go`
- Modify: `plugins/connector/git/policy_test.go`

**Interfaces:**
- Consumes: `EffectivePolicy`, `Resolve`, `splitCSV`, `MatchRepo` from Task 1.
- Produces:
  - `type Request struct { Op, Branch, Remote string; Force, NewBranch bool; RawSubcommand string }`
  - `type Verdict struct { Allow bool; Reason, MatchedRule string }`
  - `func IsProtected(p EffectivePolicy, branch string) bool`
  - `func (p EffectivePolicy) Evaluate(r Request) Verdict`
  - `var mutatingOps map[string]bool`

- [ ] **Step 1: Write the failing test for `IsProtected`**

```go
func TestIsProtected(t *testing.T) {
	p := EffectivePolicy{Protected: []string{"master", "main", "release/*"}}
	cases := map[string]bool{
		"master":          true,
		"main":            true,
		"release/2024-01": true,
		"release":         false, // release/* needs a segment after the slash
		"fix/login":       false,
		"Master":          true, // branch names compared case-insensitively
	}
	for branch, want := range cases {
		if got := IsProtected(p, branch); got != want {
			t.Errorf("IsProtected(%q) = %v, want %v", branch, got, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugins/connector/git/ -run TestIsProtected -v`
Expected: FAIL — `undefined: IsProtected`

- [ ] **Step 3: Implement `IsProtected`**

```go
// IsProtected reports whether a branch matches any protected glob. Comparison
// is case-insensitive because git branch names are case-sensitive on Linux but
// not on Windows/macOS checkouts — treating "Master" as unprotected would be a
// trivial bypass.
func IsProtected(p EffectivePolicy, branch string) bool {
	b := strings.ToLower(strings.TrimSpace(branch))
	if b == "" {
		return false
	}
	for _, glob := range p.Protected {
		if ok, err := path.Match(strings.ToLower(glob), b); err == nil && ok {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugins/connector/git/ -run TestIsProtected -v`
Expected: PASS

- [ ] **Step 5: Write the failing test for `Evaluate`**

```go
func TestEvaluate(t *testing.T) {
	base := EffectivePolicy{
		BranchPattern: `^(fix|feat)/[a-z0-9._-]+$`,
		BranchRe:      regexp.MustCompile(`^(fix|feat)/[a-z0-9._-]+$`),
		Protected:     []string{"master", "main"},
		MatchedRule:   "global",
	}

	cases := []struct {
		name  string
		pol   EffectivePolicy
		req   Request
		allow bool
	}{
		{"push to feature branch", base, Request{Op: "push", Branch: "fix/login"}, true},
		{"push to protected master", base, Request{Op: "push", Branch: "master"}, false},
		{"push to protected main", base, Request{Op: "push", Branch: "main"}, false},
		{"new branch matching pattern", base, Request{Op: "branch_create", Branch: "feat/x", NewBranch: true}, true},
		{"new branch violating pattern", base, Request{Op: "branch_create", Branch: "temp", NewBranch: true}, false},
		{"new branch that is protected", base, Request{Op: "branch_create", Branch: "master", NewBranch: true}, false},
		{"force push denied by default", base, Request{Op: "push", Branch: "fix/login", Force: true}, false},
		{"read op on protected branch is fine", base, Request{Op: "log", Branch: "master"}, true},
		{"read op needs no branch", base, Request{Op: "status"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := c.pol.Evaluate(c.req)
			if v.Allow != c.allow {
				t.Errorf("Allow = %v, want %v (reason: %s)", v.Allow, c.allow, v.Reason)
			}
			if !v.Allow && v.Reason == "" {
				t.Error("a denied verdict must carry a Reason")
			}
			if v.MatchedRule == "" {
				t.Error("MatchedRule must never be empty")
			}
		})
	}
}

func TestEvaluateForcePushAllowed(t *testing.T) {
	p := EffectivePolicy{
		BranchRe:       regexp.MustCompile(`.*`),
		AllowForcePush: true,
		MatchedRule:    "per-repo → */org/sandbox",
	}
	if v := p.Evaluate(Request{Op: "push", Branch: "fix/x", Force: true}); !v.Allow {
		t.Errorf("force push denied despite AllowForcePush: %s", v.Reason)
	}
}

func TestEvaluatePolicyErrDeniesMutationsOnly(t *testing.T) {
	p := EffectivePolicy{PolicyErr: "branch pattern does not compile: x", MatchedRule: "global"}

	if v := p.Evaluate(Request{Op: "push", Branch: "fix/x"}); v.Allow {
		t.Error("push allowed despite malformed policy — must fail closed")
	}
	if v := p.Evaluate(Request{Op: "status"}); !v.Allow {
		t.Errorf("read op denied by malformed policy: %s — reads must still work", v.Reason)
	}
}

func TestEvaluateRaw(t *testing.T) {
	t.Run("disabled denies everything", func(t *testing.T) {
		p := EffectivePolicy{RawEnabled: false, MatchedRule: "global",
			RawRules: map[string]string{"bisect": "allow"}}
		if v := p.Evaluate(Request{Op: "raw", RawSubcommand: "bisect"}); v.Allow {
			t.Error("raw allowed while raw_enabled = false")
		}
	})

	t.Run("enabled with empty rules denies everything", func(t *testing.T) {
		p := EffectivePolicy{RawEnabled: true, MatchedRule: "global"}
		if v := p.Evaluate(Request{Op: "raw", RawSubcommand: "bisect"}); v.Allow {
			t.Error("raw allowed with no rules — must fail closed")
		}
	})

	t.Run("unlisted subcommand denied", func(t *testing.T) {
		p := EffectivePolicy{RawEnabled: true, MatchedRule: "global",
			RawRules: map[string]string{"bisect": "allow"}}
		if v := p.Evaluate(Request{Op: "raw", RawSubcommand: "push"}); v.Allow {
			t.Error("unlisted subcommand allowed — must fail closed")
		}
	})

	t.Run("allow wins for listed subcommand", func(t *testing.T) {
		p := EffectivePolicy{RawEnabled: true, MatchedRule: "global",
			RawRules: map[string]string{"bisect": "allow"}}
		if v := p.Evaluate(Request{Op: "raw", RawSubcommand: "bisect"}); !v.Allow {
			t.Errorf("allowed subcommand denied: %s", v.Reason)
		}
	})

	t.Run("deny beats allow", func(t *testing.T) {
		p := EffectivePolicy{RawEnabled: true, MatchedRule: "global",
			RawRules: map[string]string{"push": "deny"}}
		if v := p.Evaluate(Request{Op: "raw", RawSubcommand: "push"}); v.Allow {
			t.Error("deny rule did not win")
		}
	})

	t.Run("empty subcommand denied", func(t *testing.T) {
		p := EffectivePolicy{RawEnabled: true, MatchedRule: "global",
			RawRules: map[string]string{"bisect": "allow"}}
		if v := p.Evaluate(Request{Op: "raw", RawSubcommand: ""}); v.Allow {
			t.Error("empty subcommand allowed — must fail closed")
		}
	})
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./plugins/connector/git/ -run TestEvaluate -v`
Expected: FAIL — `undefined: Request`, `undefined: Verdict`

- [ ] **Step 7: Implement `Evaluate`**

```go
// Request is one operation being judged. Branch is the target branch for a push
// or the current branch for a commit; NewBranch marks branch creation so the
// name pattern applies.
type Request struct {
	Op            string
	Branch        string
	Remote        string
	Force         bool
	NewBranch     bool
	RawSubcommand string
}

// Verdict is the policy outcome. A denied verdict always carries a Reason, and
// MatchedRule always names the layer that decided, so the manager UI and the
// operation response can both explain themselves.
type Verdict struct {
	Allow       bool
	Reason      string
	MatchedRule string
}

// mutatingOps are the operations that change repository or remote state. Only
// these are blocked when the policy config is malformed — reads stay available
// so an admin can still inspect the repo while fixing the config.
var mutatingOps = map[string]bool{
	"push": true, "commit": true, "merge": true, "reset": true, "rebase": true,
	"branch_create": true, "checkout": true, "add": true, "stash": true,
	"stash_drop": true, "tag": true, "tag_delete": true, "pull": true,
	"clone": true, "raw": true,
}

// Evaluate judges a request against the compiled policy. It never touches the
// filesystem or spawns a process, so a denial costs nothing.
func (p EffectivePolicy) Evaluate(r Request) Verdict {
	deny := func(reason string) Verdict {
		return Verdict{Allow: false, Reason: reason, MatchedRule: p.MatchedRule}
	}
	allow := func() Verdict {
		return Verdict{Allow: true, MatchedRule: p.MatchedRule}
	}

	// Malformed config blocks mutations and only mutations.
	if p.PolicyErr != "" {
		if mutatingOps[r.Op] {
			return deny("policy config is invalid, mutating operations are blocked: " + p.PolicyErr)
		}
		return allow()
	}

	if r.Op == "raw" {
		return p.evaluateRaw(r, deny, allow)
	}

	// Force flags need an explicit opt-in, whatever the branch.
	if r.Force && !p.AllowForcePush {
		return deny("force push is not allowed by this policy (allow_force_push is off)")
	}

	// Branch rules apply to mutations only — reading a protected branch is fine.
	if mutatingOps[r.Op] {
		if IsProtected(p, r.Branch) {
			return deny(fmt.Sprintf("branch %q is protected; direct %s is blocked", r.Branch, r.Op))
		}
		if r.NewBranch && p.BranchRe != nil && !p.BranchRe.MatchString(r.Branch) {
			return deny(fmt.Sprintf("branch %q does not match the required pattern %s",
				r.Branch, p.BranchPattern))
		}
	}
	return allow()
}

// evaluateRaw gates the raw operation. Both the master switch and an explicit
// per-subcommand allow are required; an unlisted subcommand is denied.
func (p EffectivePolicy) evaluateRaw(r Request, deny func(string) Verdict, allow func() Verdict) Verdict {
	if !p.RawEnabled {
		return deny("the raw operation is disabled for this connector")
	}
	sub := strings.ToLower(strings.TrimSpace(r.RawSubcommand))
	if sub == "" {
		return deny("no git subcommand could be determined from the arguments")
	}
	switch p.RawRules[sub] {
	case "deny":
		return deny(fmt.Sprintf("subcommand %q is explicitly denied", sub))
	case "allow":
		return allow()
	default:
		return deny(fmt.Sprintf("subcommand %q is not in the allow list", sub))
	}
}
```

- [ ] **Step 8: Run test to verify it passes**

Run: `go test ./plugins/connector/git/ -v`
Expected: PASS — every test in Task 1 and Task 2

- [ ] **Step 9: Commit**

```bash
git add plugins/connector/git/policy.go plugins/connector/git/policy_test.go
git commit -m "feat(git): policy evaluation with fail-closed raw gating"
```

---

### Task 3: Remote URL handling

**Files:**
- Create: `plugins/connector/git/remote.go`
- Test: `plugins/connector/git/remote_test.go`

**Interfaces:**
- Consumes: `ParseKVList` from Task 1.
- Produces:
  - `type RemoteInfo struct { Original, Effective, Slug string; Converted bool }`
  - `func ParseHostMap(jsonStr string) map[string]string`
  - `func StripCredentials(rawURL string) string`
  - `func ConvertRemote(rawURL string, hostMap map[string]string, convertSSH bool) (RemoteInfo, error)`
  - `func RepoSlug(rawURL string) string`

- [ ] **Step 1: Write the failing test for `StripCredentials`**

```go
package main

import "testing"

func TestStripCredentials(t *testing.T) {
	cases := map[string]string{
		"https://user:token@github.com/org/repo.git": "https://github.com/org/repo.git",
		"https://token@github.com/org/repo.git":      "https://github.com/org/repo.git",
		"https://github.com/org/repo.git":            "https://github.com/org/repo.git",
		"https://user:p%40ss@abc.com/org/repo.git":   "https://abc.com/org/repo.git",
		"git@github.com:org/repo.git":                "git@github.com:org/repo.git", // scp form untouched
		"":                                           "",
	}
	for in, want := range cases {
		if got := StripCredentials(in); got != want {
			t.Errorf("StripCredentials(%q) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./plugins/connector/git/ -run TestStripCredentials -v`
Expected: FAIL — `undefined: StripCredentials`

- [ ] **Step 3: Implement `StripCredentials`**

```go
// remote.go handles remote URLs: reading credentials out of them, converting SSH
// remotes to HTTPS, and reporting what was actually used.
//
// The plugin never rewrites .git/config. Credentials baked into a remote URL are
// ignored, not consumed and not removed — the clean URL is passed explicitly to
// each network operation instead.

import (
	"fmt"
	"net/url"
	"strings"
)

// StripCredentials removes any user:password@ prefix from an http(s) URL. The
// scp-style form (git@host:path) is returned untouched — its "git@" is a
// transport username, not a credential, and ConvertRemote handles it.
func StripCredentials(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "http") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	u.User = nil
	return u.String()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./plugins/connector/git/ -run TestStripCredentials -v`
Expected: PASS

- [ ] **Step 5: Write the failing test for `ConvertRemote`**

```go
func TestConvertRemote(t *testing.T) {
	cases := []struct {
		name      string
		in        string
		want      string
		converted bool
	}{
		{"scp form github", "git@github.com:org/repo.git", "https://github.com/org/repo.git", true},
		{"ssh scheme", "ssh://git@github.com/org/repo.git", "https://github.com/org/repo.git", true},
		{"ssh scheme with port", "ssh://git@github.com:22/org/repo.git", "https://github.com/org/repo.git", true},
		{"bitbucket scp", "git@bitbucket.org:team/repo.git", "https://bitbucket.org/team/repo.git", true},
		{"gitlab nested path", "git@gitlab.com:group/sub/repo.git", "https://gitlab.com/group/sub/repo.git", true},
		{"already https", "https://github.com/org/repo.git", "https://github.com/org/repo.git", false},
		{"https with credentials stripped", "https://u:t@github.com/org/repo.git", "https://github.com/org/repo.git", false},
		{"no .git suffix is preserved as-is", "git@github.com:org/repo", "https://github.com/org/repo", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ConvertRemote(c.in, nil, true)
			if err != nil {
				t.Fatalf("ConvertRemote(%q): %v", c.in, err)
			}
			if got.Effective != c.want {
				t.Errorf("Effective = %q, want %q", got.Effective, c.want)
			}
			if got.Converted != c.converted {
				t.Errorf("Converted = %v, want %v", got.Converted, c.converted)
			}
			if got.Original != c.in {
				t.Errorf("Original = %q, want the input preserved", got.Original)
			}
		})
	}
}

func TestConvertRemoteHostMap(t *testing.T) {
	hostMap := map[string]string{
		"git.internal": "code.company.com/git",
		"ssh.abc.net":  "abc.net",
	}
	got, err := ConvertRemote("git@git.internal:team/api.git", hostMap, true)
	if err != nil {
		t.Fatalf("ConvertRemote: %v", err)
	}
	if got.Effective != "https://code.company.com/git/team/api.git" {
		t.Errorf("Effective = %q, want the mapped host with its path prefix", got.Effective)
	}
}

func TestConvertRemoteMustFail(t *testing.T) {
	t.Run("ssh config alias", func(t *testing.T) {
		// "myserver" has no dot: it cannot be a real hostname, so it is an alias
		// from ~/.ssh/config whose real host we cannot know.
		_, err := ConvertRemote("myserver:org/repo.git", nil, true)
		if err == nil {
			t.Fatal("expected an error for an ssh config alias")
		}
		if !strings.Contains(err.Error(), "myserver") {
			t.Errorf("error must name the alias, got: %v", err)
		}
		if !strings.Contains(err.Error(), "remote_host_map") {
			t.Errorf("error must point at remote_host_map, got: %v", err)
		}
	})

	t.Run("conversion disabled", func(t *testing.T) {
		_, err := ConvertRemote("git@github.com:org/repo.git", nil, false)
		if err == nil {
			t.Fatal("expected an error when conversion is disabled for an SSH remote")
		}
		if !strings.Contains(err.Error(), "convert_ssh_remote_to_https") {
			t.Errorf("error must name the setting, got: %v", err)
		}
	})
}

func TestRepoSlug(t *testing.T) {
	cases := map[string]string{
		"https://github.com/org/repo.git":   "github.com/org/repo",
		"git@github.com:org/repo.git":       "github.com/org/repo",
		"https://u:t@abc.com/org/repo.git":  "abc.com/org/repo",
		"git@gitlab.com:group/sub/repo.git": "gitlab.com/group/sub/repo",
		"":                                  "",
	}
	for in, want := range cases {
		if got := RepoSlug(in); got != want {
			t.Errorf("RepoSlug(%q) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 6: Run test to verify it fails**

Run: `go test ./plugins/connector/git/ -run 'TestConvertRemote|TestRepoSlug' -v`
Expected: FAIL — `undefined: ConvertRemote`, `undefined: RepoSlug`

- [ ] **Step 7: Implement `ConvertRemote`, `ParseHostMap`, `RepoSlug`**

Add `"strings"` to the import block if not already present.

```go
// RemoteInfo records what URL an operation actually used. Every network
// operation reports it, so a push landing on an unexpected host is visible
// immediately instead of being a mystery.
type RemoteInfo struct {
	Original  string
	Effective string
	Slug      string // host/owner/repo, used for policy matching
	Converted bool
}

// ParseHostMap decodes the remote_host_map kvlist into ssh_host → https_host.
// A malformed value yields an empty map rather than an error: the caller then
// falls back to mechanical conversion, which is correct for the cloud providers.
func ParseHostMap(s string) map[string]string {
	rows, err := ParseKVList(s)
	if err != nil {
		return nil
	}
	out := make(map[string]string, len(rows))
	for _, r := range rows {
		ssh := strings.TrimSpace(r["ssh_host"])
		https := strings.TrimSpace(r["https_host"])
		if ssh != "" && https != "" {
			out[strings.ToLower(ssh)] = https
		}
	}
	return out
}

// ConvertRemote turns a remote URL into the HTTPS URL a network operation should
// use. HTTPS input is only credential-stripped; SSH input is converted when
// convertSSH is true.
//
// Two shapes deliberately fail instead of guessing:
//   - an ~/.ssh/config Host alias, whose real hostname is unknowable here
//   - conversion disabled while the remote is SSH
func ConvertRemote(raw string, hostMap map[string]string, convertSSH bool) (RemoteInfo, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return RemoteInfo{}, fmt.Errorf("remote URL is empty")
	}

	info := RemoteInfo{Original: raw}

	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		info.Effective = StripCredentials(raw)
		info.Slug = RepoSlug(info.Effective)
		return info, nil
	}

	host, repoPath, err := splitSSHRemote(raw)
	if err != nil {
		return RemoteInfo{}, err
	}

	if !convertSSH {
		return RemoteInfo{}, fmt.Errorf(
			"remote %q uses SSH but convert_ssh_remote_to_https is off; enable it or set an HTTPS remote", raw)
	}

	target := host
	if mapped, ok := hostMap[strings.ToLower(host)]; ok {
		target = mapped
	} else if !strings.Contains(host, ".") {
		// No dot means this cannot be a real hostname — it is an ~/.ssh/config
		// Host alias. Guessing would silently push to the wrong server.
		return RemoteInfo{}, fmt.Errorf(
			"remote host %q looks like an ~/.ssh/config alias, not a hostname; add a remote_host_map row mapping it to the HTTPS host", host)
	}

	info.Effective = "https://" + strings.TrimRight(target, "/") + "/" + repoPath
	info.Slug = RepoSlug(info.Effective)
	info.Converted = true
	return info, nil
}

// splitSSHRemote extracts host and repo path from either SSH form:
//
//	git@host:owner/repo.git          (scp-like)
//	ssh://git@host[:port]/owner/repo.git
//
// The repo path keeps every segment, so GitLab subgroups survive.
func splitSSHRemote(raw string) (host, repoPath string, err error) {
	if strings.HasPrefix(raw, "ssh://") {
		u, perr := url.Parse(raw)
		if perr != nil {
			return "", "", fmt.Errorf("parse ssh remote %q: %w", raw, perr)
		}
		return u.Hostname(), strings.TrimLeft(u.Path, "/"), nil
	}

	at := strings.Index(raw, "@")
	colon := strings.Index(raw, ":")
	if colon < 0 {
		return "", "", fmt.Errorf("remote %q is not a recognised git URL", raw)
	}
	host = raw[at+1 : colon] // at == -1 gives raw[0:colon], which is what we want
	repoPath = strings.TrimLeft(raw[colon+1:], "/")
	if host == "" || repoPath == "" {
		return "", "", fmt.Errorf("remote %q is not a recognised git URL", raw)
	}
	return host, repoPath, nil
}

// RepoSlug reduces any remote URL to host/owner/repo (no scheme, no credentials,
// no .git suffix) for policy matching.
func RepoSlug(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var host, p string
	switch {
	case strings.HasPrefix(raw, "http://"), strings.HasPrefix(raw, "https://"):
		u, err := url.Parse(raw)
		if err != nil {
			return ""
		}
		host, p = u.Hostname(), strings.TrimLeft(u.Path, "/")
	default:
		h, rp, err := splitSSHRemote(raw)
		if err != nil {
			return ""
		}
		host, p = h, rp
	}
	return host + "/" + strings.TrimSuffix(p, ".git")
}
```

- [ ] **Step 8: Run the full test suite**

Run: `go test ./plugins/connector/git/ -v`
Expected: PASS — policy and remote tests

- [ ] **Step 9: Verify `go vet` is clean**

Run: `go vet ./plugins/connector/git/`
Expected: no output

- [ ] **Step 10: Commit**

```bash
git add plugins/connector/git/remote.go plugins/connector/git/remote_test.go
git commit -m "feat(git): remote URL conversion with explicit failure on ssh aliases"
```

---

**Part 1 complete.** Continue with [impl-2-runner.md](impl-2-runner.md) — the git runner, operations, and plugin entry point.
