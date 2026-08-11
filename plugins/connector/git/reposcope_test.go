package main

import (
	"strings"
	"testing"
)

// This file covers the fail-open hole in policy scope resolution.
//
// A per-repo rule can be matched against the local path OR against host/owner/repo,
// and the slug half comes from a remote URL. When that URL cannot be read the slug is
// empty, a rule like "*/org/infra" cannot match, and resolution falls through to the
// GLOBAL fallback — which on a connector whose protection lives entirely in per-repo
// rules is wide open: no branch pattern, nothing protected, direct push to main
// allowed.
//
// The fix distinguishes the two reasons the slug can be missing, because they are not
// the same failure:
//
//   - The named remote does not exist → an error. The caller asked about something
//     that is not there, and answering a typo with the permissive fallback is exactly
//     the trap.
//   - The repository has no remote at all → proceed. That is a legitimate local-only
//     checkout and path rules still match it. What was wrong before was the SILENCE,
//     so the response carries policy.unresolved_scope naming what could not be
//     evaluated.

// TestRepoScopeErrorsOnAMissingRemote is the first half: a name that is not there must
// not be answered with the fallback policy.
func TestRepoScopeErrorsOnAMissingRemote(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	runInRepo(t, dir, "remote", "add", "origin", "https://abc.com/org/api.git")

	_, _, err := repoScope(opCtx(nil, nil), dir, "does-not-exist")
	if err == nil {
		t.Fatal("a remote that does not exist must be an error, not a silent fallback")
	}
	// Naming the real remotes turns "that is wrong" into "here is what to use", and a
	// caller has no other way to find them when remote_list is disabled.
	if !strings.Contains(err.Error(), "origin") {
		t.Errorf("the error must name the repository's real remotes, got: %v", err)
	}
}

// TestRepoScopeAllowsARepoWithNoRemote is the second half. Refusing here would break a
// legitimate local-only repository for no safety gain — path rules still match it.
func TestRepoScopeAllowsARepoWithNoRemote(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)

	slug, warn, err := repoScope(opCtx(nil, nil), dir, "")
	if err != nil {
		t.Fatalf("a repository with no remote is a normal state, got: %v", err)
	}
	if slug != "" {
		t.Errorf("slug = %q, want empty when there is no remote", slug)
	}
	// Nothing stricter is configured, so there is nothing to warn about — a warning
	// here would be noise that trains the reader to ignore the field.
	if warn != "" {
		t.Errorf("warn = %q, want silence when no rule was left unevaluated", warn)
	}
}

// TestRepoScopeWarnsWhenStricterRulesCannotBeEvaluated is the reportable half of the
// hole: the fallback IS used, and the response has to say which rules were skipped.
func TestRepoScopeWarnsWhenStricterRulesCannotBeEvaluated(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	cfg := map[string]string{
		// Stricter than the fallback (which protects nothing) and matchable only by slug.
		"repo_policies": `[{"repo":"*/org/infra","protected":"main,master"}]`,
	}

	_, warn, err := repoScope(opCtx(cfg, nil), dir, "")
	if err != nil {
		t.Fatalf("repoScope: %v", err)
	}
	if warn == "" {
		t.Fatal("a stricter slug-only rule that could not be evaluated must be reported")
	}
	if !strings.Contains(warn, "*/org/infra") {
		t.Errorf("the warning must name the rule that was skipped, got %q", warn)
	}
	if !strings.Contains(warn, "more permissive") {
		t.Errorf("the warning must say the fallback may be more permissive, got %q", warn)
	}
}

// TestScopeWarningIgnoresRulesThatWouldNotTighten keeps the warning honest. Reporting
// every unevaluable rule would fire on rules that have nothing to do with this
// repository, and a field that cries wolf is a field nobody reads.
func TestScopeWarningIgnoresRulesThatWouldNotTighten(t *testing.T) {
	g := GlobalPolicy{
		BranchPattern: `^fix/.+$`,
		Protected:     []string{"main"},
	}
	for _, tc := range []struct {
		name string
		rule RepoRule
		warn bool
	}{
		// Loosenings: a permissive answer cannot be made wrong by a rule that would have
		// permitted more.
		{"clears the branch pattern", RepoRule{Repo: "*/org/x", BranchPattern: "-"}, false},
		{"clears the protected list", RepoRule{Repo: "*/org/x", Protected: "-"}, false},
		{"allows force push", RepoRule{Repo: "*/org/x", ForcePush: "true"}, false},
		{"repeats the fallback pattern", RepoRule{Repo: "*/org/x", BranchPattern: `^fix/.+$`}, false},
		{"protects only what the fallback already does", RepoRule{Repo: "*/org/x", Protected: "main"}, false},

		// Tightenings: these are the ones a missing slug makes dangerous.
		{"a different branch pattern", RepoRule{Repo: "*/org/x", BranchPattern: `^ops/.+$`}, true},
		{"a new commit message rule", RepoRule{Repo: "*/org/x", MessagePattern: `^X-.+`}, true},
		{"protects something new", RepoRule{Repo: "*/org/x", Protected: "main,release/*"}, true},

		// Not slug-shaped: a path glob failing to match has nothing to do with the slug,
		// so warning about it would point at the wrong cause.
		{"a path glob", RepoRule{Repo: "d:/code/*", BranchPattern: `^ops/.+$`}, false},
		{"too few segments", RepoRule{Repo: "org/*", BranchPattern: `^ops/.+$`}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := scopeWarning(g, []RepoRule{tc.rule}, "d:/elsewhere/repo", "")
			if (got != "") != tc.warn {
				t.Errorf("warning = %q, want warn=%v", got, tc.warn)
			}
		})
	}

	// Force push: denying where the fallback allows IS a tightening.
	permissive := GlobalPolicy{AllowForcePush: true}
	if scopeWarning(permissive, []RepoRule{{Repo: "*/org/x", ForcePush: "false"}}, "d:/e/r", "") == "" {
		t.Error("a rule denying force push where the fallback allows it is a tightening")
	}

	// And a resolved slug silences everything: there is nothing unevaluable.
	if got := scopeWarning(g, []RepoRule{{Repo: "*/org/x", BranchPattern: `^ops/.+$`}},
		"d:/e/r", "abc.com/org/other"); got != "" {
		t.Errorf("a readable slug leaves nothing to warn about, got %q", got)
	}
}

// TestOperationsReportUnresolvedScope closes the loop: a warning computed but never
// surfaced would leave the hole exactly as silent as it was.
func TestOperationsReportUnresolvedScope(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	cfg := baseCfg()
	// Stricter than baseCfg, which already protects main and master: this rule adds a
	// branch pattern the fallback does not have. A rule that merely repeated the
	// fallback's protected list would not be a tightening and must not warn.
	cfg["repo_policies"] = `[{"repo":"*/org/infra","branch_pattern":"^ops/only$"}]`

	// A name the FALLBACK accepts, so the only thing that could refuse it is the rule
	// that could not be evaluated — which is the case under test.
	m := envelopeOf(t)(doBranchCreate(opCtx(cfg, map[string]string{
		"repo_path": dir, "name": "fix/scope-check",
	})))
	pol := policyOf(t, m)

	// The operation is ALLOWED — that is the deliberate choice, since a local-only
	// repository is legitimate — but it must not be allowed silently.
	if pol["verdict"] != "allow" {
		t.Fatalf("a repository with no remote must still be usable, got %v", pol)
	}
	warn, _ := pol["unresolved_scope"].(string)
	if warn == "" {
		t.Error("the response must report that stricter rules could not be evaluated")
	}
	if !strings.Contains(warn, "*/org/infra") {
		t.Errorf("the warning must name the skipped rule, got %q", warn)
	}
}

// TestOperationsOmitUnresolvedScopeWhenThereIsNothingToSay pairs with the test above.
// The field's presence is the signal, so it must be absent in the normal case.
func TestOperationsOmitUnresolvedScopeWhenThereIsNothingToSay(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	runInRepo(t, dir, "remote", "add", "origin", "https://abc.com/org/api.git")

	cfg := baseCfg()
	cfg["repo_policies"] = `[{"repo":"*/org/infra","branch_pattern":"^ops/only$"}]`

	m := envelopeOf(t)(doBranchCreate(opCtx(cfg, map[string]string{
		"repo_path": dir, "name": "fix/scope-check",
	})))
	if _, present := policyOf(t, m)["unresolved_scope"]; present {
		t.Error("a resolvable slug must leave the warning out entirely")
	}
}

// TestPolicyShowSharesTheOperationsScopeRule is the consistency property. policy_show
// exists to predict what the operations do, so a second copy of the missing-slug rule
// could disagree with them — which is the failure mode the operation was written to
// prevent in the first place.
func TestPolicyShowSharesTheOperationsScopeRule(t *testing.T) {
	requireGit(t)
	dir := initTestRepo(t)
	runInRepo(t, dir, "remote", "add", "origin", "https://abc.com/org/api.git")

	// A remote that does not exist: an error from both.
	if _, err := doPolicyShow(testWidgetCtx(nil,
		map[string]string{"repo": dir, "remote": "nope"})); err == nil {
		t.Error("policy_show must error on a missing remote, like the operations do")
	}
	if _, _, err := repoScope(opCtx(nil, nil), dir, "nope"); err == nil {
		t.Error("repoScope must error on a missing remote")
	}

	// No remote at all: both proceed, and policy_show explains the cost.
	bare := initTestRepo(t)
	cfg := map[string]string{"repo_policies": `[{"repo":"*/org/infra","branch_pattern":"^ops/only$"}]`}
	out, err := doPolicyShow(testWidgetCtx(cfg, map[string]string{"repo": bare}))
	if err != nil {
		t.Fatalf("policy_show must not refuse a repository with no remote: %v", err)
	}
	note, _ := out.(map[string]any)["note"].(string)
	if !strings.Contains(note, "*/org/infra") {
		t.Errorf("policy_show must name the rules it could not evaluate, got %q", note)
	}
}
