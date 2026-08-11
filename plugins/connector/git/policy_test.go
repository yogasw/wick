package main

import (
	"regexp"
	"strings"
	"testing"
)

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
		"*/org/sandbox":       1,
		"*/org/*":             2,
		"github.com/org/repo": 0,
		"*":                   1,
		"d:/code/work/*":      1,
	}
	for glob, want := range cases {
		if got := Specificity(glob); got != want {
			t.Errorf("Specificity(%q) = %d, want %d", glob, got, want)
		}
	}
}

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

func TestResolveTieStrictnessCannotBreakIsStillDeterministic(t *testing.T) {
	global := GlobalPolicy{Protected: []string{"master"}}
	// Same wildcard count, same ForcePush, same protected-list length. Strictness
	// cannot separate these, so the lexicographic fallback must decide — and the
	// answer must not change when the rows are reordered.
	rules := []RepoRule{
		{Repo: "*/org/api", BranchPattern: `^aaa/.+$`, Protected: "main", ForcePush: "true"},
		{Repo: "github.com/*/api", BranchPattern: `^zzz/.+$`, Protected: "dev", ForcePush: "true"},
	}

	forward := Resolve(global, rules, "d:/code/api", "github.com/org/api")
	reversed := Resolve(global, []RepoRule{rules[1], rules[0]}, "d:/code/api", "github.com/org/api")

	if forward.BranchPattern != reversed.BranchPattern {
		t.Errorf("resolution depends on row order: %q vs %q",
			forward.BranchPattern, reversed.BranchPattern)
	}
	if forward.MatchedRule != reversed.MatchedRule {
		t.Errorf("MatchedRule depends on row order: %q vs %q",
			forward.MatchedRule, reversed.MatchedRule)
	}
	// "*/org/api" sorts before "github.com/*/api" ('*' < 'g').
	if forward.BranchPattern != `^aaa/.+$` {
		t.Errorf("BranchPattern = %q, want the lexicographically first glob to win",
			forward.BranchPattern)
	}
}

func TestResolveForcePushDashInherits(t *testing.T) {
	// A boolean has no "cleared" state, so "-" must behave exactly like empty.
	for _, marker := range []string{"", "-"} {
		for _, globalAllows := range []bool{true, false} {
			global := GlobalPolicy{AllowForcePush: globalAllows}
			rules := []RepoRule{{Repo: "*/org/api", ForcePush: marker}}
			got := Resolve(global, rules, "d:/code/api", "github.com/org/api")
			if got.AllowForcePush != globalAllows {
				t.Errorf("ForcePush=%q with global=%v gave %v, want the global value inherited",
					marker, globalAllows, got.AllowForcePush)
			}
		}
	}
}

func TestResolveBranchPatternDashClears(t *testing.T) {
	global := GlobalPolicy{BranchPattern: `^(fix|feat)/.+$`}
	rules := []RepoRule{{Repo: "*/org/api", BranchPattern: "-"}}
	got := Resolve(global, rules, "d:/code/api", "github.com/org/api")

	if got.BranchPattern != "" {
		t.Errorf("BranchPattern = %q, want cleared by \"-\"", got.BranchPattern)
	}
	if got.BranchRe != nil {
		t.Error("BranchRe must be nil once the pattern is cleared")
	}
}

func TestParseRepoRules(t *testing.T) {
	in := `[
		{"repo":"*/org/infra","branch_pattern":"^ops/.+$","protected":"master,main","force_push":"false"},
		{"repo":"  */org/api  ","branch_pattern":"  ","protected":"-","force_push":""},
		{"repo":"","branch_pattern":"^x$"}
	]`
	rules, err := ParseRepoRules(in)
	if err != nil {
		t.Fatalf("ParseRepoRules: %v", err)
	}

	// The third row has no repo glob, so it can never match and is dropped.
	if len(rules) != 2 {
		t.Fatalf("got %d rules, want 2 (the row with an empty repo must be dropped)", len(rules))
	}
	if rules[0].Repo != "*/org/infra" || rules[0].BranchPattern != `^ops/.+$` {
		t.Errorf("rule 0 = %+v, want the values preserved verbatim", rules[0])
	}
	if rules[1].Repo != "*/org/api" {
		t.Errorf("rule 1 Repo = %q, want surrounding whitespace trimmed", rules[1].Repo)
	}
	if rules[1].BranchPattern != "" {
		t.Errorf("rule 1 BranchPattern = %q, want whitespace-only treated as empty", rules[1].BranchPattern)
	}
	if rules[1].Protected != "-" {
		t.Errorf("rule 1 Protected = %q, want the clear marker preserved", rules[1].Protected)
	}
}

func TestParseRepoRulesInvalid(t *testing.T) {
	if _, err := ParseRepoRules(`{"repo":"x"}`); err == nil {
		t.Fatal("expected an error for an object instead of an array")
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

func TestEvaluateForceOnReadOpIsIgnored(t *testing.T) {
	// The service layer fills Request generically, so a read op can arrive with
	// Force set. Force is a mutation concern: denying a log or a diff over it
	// would block a harmless read for no reason.
	p := EffectivePolicy{
		Protected:      []string{"master"},
		AllowForcePush: false,
		MatchedRule:    "global",
	}
	for _, op := range []string{"log", "diff", "status", "show", "branch_list"} {
		v := p.Evaluate(Request{Op: op, Branch: "master", Force: true})
		if !v.Allow {
			t.Errorf("read op %q denied with Force set: %s", op, v.Reason)
		}
	}

	// The same flag on a mutation is still refused.
	if v := p.Evaluate(Request{Op: "push", Branch: "fix/x", Force: true}); v.Allow {
		t.Error("force push allowed while allow_force_push is off")
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

func TestEvaluateCommitMessagePattern(t *testing.T) {
	p := EffectivePolicy{
		MessagePattern: `^(feat|fix|chore)(\(.+\))?: .+`,
		MessageRe:      regexp.MustCompile(`^(feat|fix|chore)(\(.+\))?: .+`),
		MatchedRule:    "global",
	}

	t.Run("a conforming message passes", func(t *testing.T) {
		for _, msg := range []string{"fix: stop the timeout", "feat(auth): add SSO", "chore: bump deps"} {
			if v := p.Evaluate(Request{Op: "commit", Message: msg}); !v.Allow {
				t.Errorf("message %q was rejected: %s", msg, v.Reason)
			}
		}
	})

	t.Run("a non-conforming message is refused with the pattern named", func(t *testing.T) {
		v := p.Evaluate(Request{Op: "commit", Message: "wip"})
		if v.Allow {
			t.Fatal("a message violating the pattern was accepted")
		}
		if !strings.Contains(v.Reason, p.MessagePattern) {
			t.Errorf("reason = %q, want it to quote the required pattern", v.Reason)
		}
	})

	t.Run("only commit is judged on a message", func(t *testing.T) {
		// A push carries no message of its own; judging one would refuse a push for
		// the commit that produced it, which is already past.
		for _, op := range []string{"push", "merge", "branch_create", "log"} {
			if v := p.Evaluate(Request{Op: op, Message: "wip"}); !v.Allow {
				t.Errorf("op %q was refused over a commit message: %s", op, v.Reason)
			}
		}
	})

	t.Run("an empty message is left to git", func(t *testing.T) {
		// git refuses an empty message with its own error, which is clearer than a
		// pattern mismatch would be.
		if v := p.Evaluate(Request{Op: "commit", Message: ""}); !v.Allow {
			t.Errorf("an empty message was refused by the policy: %s", v.Reason)
		}
	})
}

func TestResolveCommitMessagePatternFailsClosed(t *testing.T) {
	// A pattern that does not compile must block mutations rather than silently
	// accept everything — the same treatment a bad branch pattern gets.
	p := Resolve(GlobalPolicy{MessagePattern: `^(feat: .+`}, nil, "d:/code/api", "github.com/org/api")

	if p.PolicyErr == "" {
		t.Fatal("PolicyErr empty, want the compile error recorded")
	}
	if !strings.Contains(p.PolicyErr, "commit message") {
		t.Errorf("PolicyErr = %q, want it to name the commit message pattern", p.PolicyErr)
	}
	if p.MessageRe != nil {
		t.Error("MessageRe must be nil when the pattern does not compile")
	}
	if v := p.Evaluate(Request{Op: "commit", Message: "anything"}); v.Allow {
		t.Error("commit allowed despite a malformed policy — must fail closed")
	}
	if v := p.Evaluate(Request{Op: "status"}); !v.Allow {
		t.Error("a read op was blocked by a malformed commit pattern")
	}
}

func TestResolveCommitMessagePatternInherits(t *testing.T) {
	// The message rule is global-only for now: per-repo rows carry no message column,
	// so an override must not silently drop the global pattern.
	g := GlobalPolicy{MessagePattern: `^fix: .+`}
	rules := []RepoRule{{Repo: "*/org/api", BranchPattern: `^ops/.+$`}}

	p := Resolve(g, rules, "d:/code/api", "github.com/org/api")
	if p.MessagePattern != `^fix: .+` {
		t.Errorf("MessagePattern = %q, want the global value preserved under a per-repo override",
			p.MessagePattern)
	}
	if p.MessageRe == nil {
		t.Error("MessageRe was not compiled")
	}
}
