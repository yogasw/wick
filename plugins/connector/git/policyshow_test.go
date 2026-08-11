package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/safeexec"
)

// tempRepo makes a directory ValidateRepoPath accepts. A .git entry is all it
// checks, and policy_show never runs git, so a real repository would only make the
// tests slower.
func tempRepo(t *testing.T) string {
	t.Helper()
	return tempRepoNamed(t, "repo")
}

// tempRepoNamed is tempRepo with a controlled last path segment, so a test can make
// a repo glob match by path rather than by remote URL.
func tempRepoNamed(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("create temp repo: %v", err)
	}
	return dir
}

// showFor runs policy_show against a temp repo, so ValidateRepoPath is satisfied
// without any test needing to know how a repo is faked.
func showFor(t *testing.T, cfg map[string]string) map[string]any {
	t.Helper()
	repo := tempRepo(t)
	out, err := doPolicyShow(testWidgetCtx(cfg, map[string]string{"repo": repo}))
	if err != nil {
		t.Fatalf("doPolicyShow: %v", err)
	}
	return out.(map[string]any)
}

func rule(t *testing.T, res map[string]any, name string) map[string]any {
	t.Helper()
	rules, ok := res["rules"].(map[string]any)
	if !ok {
		t.Fatalf("no rules block in %v", res)
	}
	r, ok := rules[name].(map[string]any)
	if !ok {
		t.Fatalf("rule %q missing from %v", name, rules)
	}
	return r
}

// TestPolicyShowReportsEveryRuleAtOnce is the whole reason the operation exists.
// Evaluate returns at the FIRST denial, so a caller working from refusals learns one
// rule per round trip and never hears about a rule it has not yet broken. One call
// here has to surface all of them.
func TestPolicyShowReportsEveryRuleAtOnce(t *testing.T) {
	res := showFor(t, map[string]string{
		"branch_name_pattern":    `^(fix|feat)/[a-z0-9-]+$`,
		"commit_message_pattern": `^(feat|fix): .+`,
		"protected_branches":     `[{"branch":"main"},{"branch":"release/*"}]`,
		"allow_force_push":       "false",
	})

	for _, name := range []string{
		"branch_pattern", "commit_message_pattern", "protected_branches", "force_push", "raw",
	} {
		r := rule(t, res, name)
		if len(r) == 0 {
			t.Errorf("rule %s is empty", name)
		}
		// Every rule says what it gates. Without it a caller has the value but not the
		// scope, which is the half that decides whether it needs to comply at all.
		if _, ok := r["applies_to"]; !ok {
			t.Errorf("rule %s does not say which operations it applies to: %v", name, r)
		}
		if _, ok := r["effect"]; !ok {
			t.Errorf("rule %s does not say what it does: %v", name, r)
		}
	}

	if got := rule(t, res, "branch_pattern")["pattern"]; got != `^(fix|feat)/[a-z0-9-]+$` {
		t.Errorf("branch pattern = %v, want the configured value", got)
	}
	if got := rule(t, res, "commit_message_pattern")["pattern"]; got != `^(feat|fix): .+` {
		t.Errorf("message pattern = %v, want the configured value", got)
	}
	prot, _ := rule(t, res, "protected_branches")["branches"].([]string)
	if len(prot) != 2 || prot[0] != "main" {
		t.Errorf("protected = %v, want both configured branches", prot)
	}
}

// TestPolicyShowNamesTheSyntaxPerRule covers the confusion the deny reason cannot
// fix: it prints "main, release/*" and "^fix/.+$" with nothing saying that the first
// is a glob list and the second a regex. A glob written into a pattern field
// compiles as a regex and then matches almost nothing, silently.
func TestPolicyShowNamesTheSyntaxPerRule(t *testing.T) {
	res := showFor(t, map[string]string{
		"branch_name_pattern":    `^fix/.+$`,
		"commit_message_pattern": `^fix: .+`,
		"protected_branches":     `[{"branch":"main"}]`,
	})

	for _, name := range []string{"branch_pattern", "commit_message_pattern"} {
		syn, _ := rule(t, res, name)["syntax"].(string)
		if !strings.Contains(syn, "RE2") {
			t.Errorf("%s must name RE2 as its language, got %q", name, syn)
		}
		// Anchoring is the failure that looks like success: unanchored "fix/" also
		// accepts "hotfix/nope", so a caller must be told.
		if !strings.Contains(syn, "^") {
			t.Errorf("%s must warn that a pattern is unanchored by default, got %q", name, syn)
		}
	}
	syn, _ := rule(t, res, "protected_branches")["syntax"].(string)
	if !strings.Contains(syn, "glob") || strings.Contains(syn, "RE2") {
		t.Errorf("protected_branches must be described as globs, not regexes, got %q", syn)
	}
}

// TestPolicyShowExampleActuallyMatches is the guard that matters most about the
// example. A wrong one is worse than none: a caller copies it, is refused, and has
// been told by the connector itself that the refusal was impossible.
func TestPolicyShowExampleActuallyMatches(t *testing.T) {
	// required marks the shapes real branch policies are actually written in. For
	// these an example is not optional: the whole point is that an agent inventing a
	// name should not have to solve the regex, and these are the regexes it will meet.
	// An exotic pattern may legitimately yield nothing.
	for _, tc := range []struct {
		pattern  string
		required bool
	}{
		{`^(fix|feat|chore)/[a-z0-9._-]+$`, true},  // the common alternation
		{`^(?:fix|feat)/[a-z0-9-]+$`, true},        // ...non-capturing
		{`^ai/(fix|feat|chore)/[a-z0-9-]+$`, true}, // a literal before the group
		{`^feature/.+$`, true},                     // no group at all
		{`.*`, true},                               // accepts everything
		{`^(fix|feat)?/?[a-z]+$`, false},           // quantified group: prefix not certain
	} {
		t.Run(tc.pattern, func(t *testing.T) {
			res := showFor(t, map[string]string{"branch_name_pattern": tc.pattern})
			ex, ok := rule(t, res, "branch_pattern")["example_accepted"].(string)
			if !ok {
				if tc.required {
					t.Fatalf("no example derived for %s, a shape branch policies are commonly written in", tc.pattern)
				}
				t.Skipf("no example derived for %s, which is allowed", tc.pattern)
			}
			re, err := regexpCompile(tc.pattern)
			if err != nil {
				t.Fatalf("test pattern does not compile: %v", err)
			}
			// The claim being checked: an example is offered as accepted, so it must be
			// accepted. A wrong one tells the caller a refusal was impossible.
			if !re.MatchString(ex) {
				t.Errorf("example %q does not match the pattern %s it claims to satisfy", ex, tc.pattern)
			}
		})
	}
}

// TestPolicyShowOffersNoExampleForAnImpossiblePattern pairs with the test above:
// silence is required where a correct example cannot be derived.
func TestPolicyShowOffersNoExampleForAnImpossiblePattern(t *testing.T) {
	// Matches nothing at all — no candidate can satisfy it.
	res := showFor(t, map[string]string{"branch_name_pattern": `^$a`})
	if ex, ok := rule(t, res, "branch_pattern")["example_accepted"]; ok {
		t.Errorf("offered example %q for a pattern nothing can match", ex)
	}
}

// TestPolicyShowSaysWhatUnsetMeans covers the state an agent misreads most. An
// empty pattern and a strict one are both "a value in a field"; only "no pattern is
// set, so any branch name is accepted" says which.
func TestPolicyShowSaysWhatUnsetMeans(t *testing.T) {
	res := showFor(t, map[string]string{})

	for _, name := range []string{"branch_pattern", "commit_message_pattern", "protected_branches"} {
		r := rule(t, res, name)
		if r["enforced"] != false {
			t.Errorf("%s must report enforced=false when unset, got %v", name, r["enforced"])
		}
		eff, _ := r["effect"].(string)
		if !strings.Contains(eff, "accepted") && !strings.Contains(eff, "allowed") {
			t.Errorf("%s must spell out what being unset permits, got %q", name, eff)
		}
	}
	// An unset pattern must not carry an example: there is nothing to satisfy, and an
	// example would read as a requirement.
	if _, ok := rule(t, res, "branch_pattern")["example_accepted"]; ok {
		t.Error("an unset pattern must not offer an example to satisfy")
	}
}

// TestPolicyShowReportsWhichRuleGatesWhichOp covers the part that cannot be
// inferred from the rules themselves, and that costs calls in both directions:
// believing the branch pattern gates a push makes a legal push look impossible,
// and believing it does not gate branch_create makes a refusal look like a bug.
func TestPolicyShowReportsWhichRuleGatesWhichOp(t *testing.T) {
	res := showFor(t, map[string]string{"branch_name_pattern": `^fix/.+$`})
	gates, ok := res["gates"].(map[string]any)
	if !ok {
		t.Fatalf("no gates block in %v", res)
	}

	branchGates, _ := gates["branch_create"].([]string)
	if !contains(branchGates, "branch_pattern") {
		t.Errorf("branch_create must be listed as gated by branch_pattern, got %v", branchGates)
	}
	// The deliberate asymmetry: the pattern stops at creation, or nobody could push
	// to any pre-existing branch whose name predates the pattern.
	pushGates, _ := gates["push"].([]string)
	for _, g := range pushGates {
		if strings.Contains(g, "branch_pattern") {
			t.Errorf("push must NOT be reported as gated by branch_pattern, got %v", pushGates)
		}
	}
	commitGates, _ := gates["commit"].([]string)
	if !contains(commitGates, "commit_message_pattern") {
		t.Errorf("commit must be listed as gated by the message pattern, got %v", commitGates)
	}
	// Reads are never gated, including under a malformed config — worth stating so a
	// caller does not avoid a diagnostic call it is allowed to make.
	if reads, _ := gates["_reads"].(string); !strings.Contains(reads, "status") {
		t.Errorf("the gates block must say which operations policy never blocks, got %q", reads)
	}
}

// TestPolicyShowResolvesPerRepository is the reason the op takes a repo_path: the
// same connector answers differently for different repositories, so a policy
// reported without one would be a guess.
func TestPolicyShowResolvesPerRepository(t *testing.T) {
	cfg := map[string]string{
		"branch_name_pattern": `^fix/.+$`,
		"repo_policies":       `[{"repo":"*/org/infra","branch_pattern":"^ops/.+$"}]`,
	}

	// A path that matches no override gets the fallback.
	plain := showFor(t, cfg)
	if got := rule(t, plain, "branch_pattern")["pattern"]; got != `^fix/.+$` {
		t.Errorf("an unmatched repo must report the fallback, got %v", got)
	}
	if got := plain["matched_rule"]; got != "global" {
		t.Errorf("matched_rule = %v, want global", got)
	}

	// A path the override matches gets the override, and says so.
	//
	// The glob ends in the repo's directory name and starts with * — one * cannot
	// cross a "/" (path.Match), and a temp dir is many segments deep, so a leading
	// "*/infra" would NOT match. "*infra" only works because MatchRepo lowercases and
	// normalises separators but does not anchor: the pattern still has to cover every
	// segment, which "*" alone does not. Matching on the trailing segment via a
	// pattern that spans the whole path is the portable way to write this.
	repo := tempRepoNamed(t, "infra")
	out, err := doPolicyShow(testWidgetCtx(
		map[string]string{
			"branch_name_pattern": `^fix/.+$`,
			"repo_policies":       `[{"repo":"` + globForPath(repo) + `","branch_pattern":"^ops/.+$"}]`,
		},
		map[string]string{"repo": repo}))
	if err != nil {
		t.Fatalf("doPolicyShow: %v", err)
	}
	res := out.(map[string]any)
	if got := rule(t, res, "branch_pattern")["pattern"]; got != `^ops/.+$` {
		t.Errorf("a matched repo must report the override, got %v", got)
	}
	if mr, _ := res["matched_rule"].(string); !strings.Contains(mr, "infra") {
		t.Errorf("matched_rule = %q, want it to name the override that decided", mr)
	}
}

// TestPolicyShowReportsAMalformedConfig covers the state where every write fails
// for a reason that is not in any rule. Without this an agent reads satisfiable
// rules, complies with all of them, and is still refused.
func TestPolicyShowReportsAMalformedConfig(t *testing.T) {
	res := showFor(t, map[string]string{"repo_policies": `[{"repo":`})

	if res["blocked"] != true {
		t.Errorf("a malformed config must be reported as blocking, got %v", res["blocked"])
	}
	reason, _ := res["blocked_reason"].(string)
	if !strings.Contains(reason, "not valid JSON") {
		t.Errorf("blocked_reason must name the problem, got %q", reason)
	}
	// Reads keep working, so say so — otherwise the only safe conclusion is that the
	// whole connector is unusable.
	if !strings.Contains(reason, "Reads still work") {
		t.Errorf("blocked_reason must say reads are unaffected, got %q", reason)
	}
}

// TestPolicyShowListsRawAllowList covers the trap in reporting raw as a boolean:
// "enabled" alone invites trying arbitrary subcommands, every one of which is denied
// unless it is on the list.
func TestPolicyShowListsRawAllowList(t *testing.T) {
	res := showFor(t, map[string]string{
		"raw_enabled": "true",
		"raw_rules":   `[{"subcommand":"describe","mode":"allow"},{"subcommand":"blame","mode":"allow"},{"subcommand":"push","mode":"deny"}]`,
	})
	raw := rule(t, res, "raw")
	if raw["enabled"] != true {
		t.Errorf("raw.enabled = %v, want true", raw["enabled"])
	}
	allowed, _ := raw["allowed_subcommands"].([]string)
	if !contains(allowed, "describe") || !contains(allowed, "blame") {
		t.Errorf("allowed_subcommands = %v, want the allow rows", allowed)
	}
	// A denied row is not an allowed one, and listing it would read as permission.
	if contains(allowed, "push") {
		t.Errorf("a deny row must not appear in allowed_subcommands: %v", allowed)
	}
	// Sorted, because map order is randomised and two identical calls must not look
	// like two different answers.
	for i := 1; i < len(allowed); i++ {
		if allowed[i] < allowed[i-1] {
			t.Errorf("allowed_subcommands must be sorted, got %v", allowed)
		}
	}

	off := showFor(t, map[string]string{"raw_enabled": "false"})
	if rule(t, off, "raw")["enabled"] != false {
		t.Error("raw must report enabled=false when the config disables it")
	}
}

// TestPolicyShowIsAReadOperation locks the property that makes it safe to call
// first: it must be agent-callable and must not be marked destructive, or a
// cautious caller would avoid the very operation that prevents blind retries.
func TestPolicyShowIsAReadOperation(t *testing.T) {
	for _, cat := range Operations() {
		for _, op := range cat.Ops {
			if op.Key != "policy_show" {
				continue
			}
			if op.Destructive {
				t.Error("policy_show must not be marked destructive — it spawns nothing")
			}
			if op.ConfigOnly {
				t.Error("policy_show must be agent-callable; ConfigOnly hides it from agents")
			}
			// The description has to say WHEN to call it. An op an agent never thinks to
			// call before acting is the same as no op at all.
			d := strings.ToLower(op.Description)
			for _, want := range []string{"before", "per repository"} {
				if !strings.Contains(d, want) {
					t.Errorf("the description must tell the agent to call it %q: %s", want, op.Description)
				}
			}
			return
		}
	}
	t.Fatal("policy_show is not registered in Operations()")
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want || strings.HasPrefix(v, want+" ") {
			return true
		}
	}
	return false
}

// globForPath builds a glob that matches one absolute path by replacing its final
// segment's parent with a wildcard per segment. path.Match's * does not cross "/",
// so a test cannot write "*infra" and expect a deep temp path to match.
func globForPath(p string) string {
	segs := strings.Split(strings.ReplaceAll(p, `\`, "/"), "/")
	for i := range segs[:len(segs)-1] {
		segs[i] = "*"
	}
	return strings.Join(segs, "/")
}

// TestGatesAndAppliesToAgree is the regression guard for a contradiction that shipped
// INSIDE one response: rules.protected_branches.applies_to listed "push, commit,
// merge and pull" while gates listed checkout and branch_create too. Both were
// hand-written prose about the same map, so they drifted, and an agent that read the
// shorter one concluded a checkout to a protected branch was safe. It is not.
func TestGatesAndAppliesToAgree(t *testing.T) {
	res := showFor(t, map[string]string{"protected_branches": `[{"branch":"main"}]`})
	gates, _ := res["gates"].(map[string]any)
	appliesTo, _ := rule(t, res, "protected_branches")["applies_to"].(string)

	// Every op gates says is subject to protected_branches must also be covered by the
	// prose, and vice versa. mutatingOps is the single source both are derived from.
	for op := range mutatingOps {
		if op == "raw" {
			continue // judged by its allow-list alone; Evaluate returns before branch checks
		}
		rules, _ := gates[op].([]string)
		if !containsExact(rules, "protected_branches") {
			t.Errorf("gates[%s] = %v, want protected_branches — Evaluate checks IsProtected for every mutating op", op, rules)
		}
		if !strings.Contains(appliesTo, op) {
			t.Errorf("applies_to does not mention %q, but that operation IS refused on a protected branch:\n%s", op, appliesTo)
		}
	}

	// The specific claim that was wrong, asserted by name so a future rewrite of the
	// sentence cannot quietly drop it again.
	if !strings.Contains(appliesTo, "checkout") {
		t.Error("applies_to must say checkout is blocked on a protected branch — that was the misreading")
	}
	// And a read must not be listed, or a caller avoids diagnostics it is allowed to run.
	for _, read := range []string{"status", "log", "diff"} {
		if containsExact(mutatingOpNames(), read) {
			t.Errorf("%s is not a mutating op and must not be gated", read)
		}
	}
}

// TestGatesNameTheConditionNotJustTheRule covers the half of a gate that a bare rule
// name loses. "push is gated by force_push" reads as "every push is refused", which
// is wrong and makes a legal push look impossible.
func TestGatesNameTheConditionNotJustTheRule(t *testing.T) {
	gates, _ := showFor(t, map[string]string{})["gates"].(map[string]any)

	for _, tc := range []struct{ op, rule, condition string }{
		{"push", "force_push", "when force"},
		{"reset", "force_push", "when mode is hard"},
		{"checkout", "branch_pattern", "when creating"},
	} {
		rules, _ := gates[tc.op].([]string)
		found := ""
		for _, r := range rules {
			if strings.HasPrefix(r, tc.rule) {
				found = r
			}
		}
		if found == "" {
			t.Errorf("gates[%s] = %v, want an entry for %s", tc.op, rules, tc.rule)
			continue
		}
		if !strings.Contains(found, tc.condition) {
			t.Errorf("gates[%s] says %q; it must state the condition (%q), or an unconditional reading is the natural one",
				tc.op, found, tc.condition)
		}
	}
	// branch_create is unconditional — the pattern always applies to a new branch — so
	// it must NOT carry a condition that would invite skipping the check.
	bc, _ := gates["branch_create"].([]string)
	if !containsExact(bc, "branch_pattern") {
		t.Errorf("gates[branch_create] = %v, want a plain branch_pattern with no condition", bc)
	}
}

// TestPolicyShowRejectsARemoteThatDoesNotExist is the most important of these. The
// operation's whole promise is "ask before you act", so answering a mistyped remote
// with the fallback policy — no branch pattern, nothing protected — is the worst
// possible failure: a policy that does not exist, delivered by the thing whose job was
// to stop the guessing. Enforcement was never bypassed, which is what made it
// dangerous; nothing downstream corrected it.
func TestPolicyShowRejectsARemoteThatDoesNotExist(t *testing.T) {
	repo := realRepoWithRemote(t, "origin", "https://abc.com/org/api.git")

	_, err := doPolicyShow(testWidgetCtx(
		map[string]string{"protected_branches": `[{"branch":"main"}]`},
		map[string]string{"repo": repo, "remote": "does-not-exist"}))
	if err == nil {
		t.Fatal("a remote that does not exist must be an error, not a silent fallback policy")
	}
	// The error has to name the remotes that DO exist: with remote_list disabled on an
	// instance, a caller has no other way to find them.
	if !strings.Contains(err.Error(), "origin") {
		t.Errorf("the error must name the repository's real remotes, got: %v", err)
	}
}

// TestPolicyShowAcceptsARepoWithNoRemote is the other half: no remotes at all is a
// legitimate state for a local-only repository, so it resolves rather than erroring —
// but the response must say the slug is empty and what that costs, instead of
// presenting a path-only match as the whole policy.
func TestPolicyShowAcceptsARepoWithNoRemote(t *testing.T) {
	repo := realRepo(t)

	out, err := doPolicyShow(testWidgetCtx(map[string]string{}, map[string]string{"repo": repo}))
	if err != nil {
		t.Fatalf("a repository with no remote is a normal state: %v", err)
	}
	res := out.(map[string]any)
	if res["repo_slug"] != "" {
		t.Errorf("repo_slug = %v, want empty when there is no remote", res["repo_slug"])
	}
	note, _ := res["note"].(string)
	if !strings.Contains(note, "no remote") {
		t.Errorf("the response must explain that host-based rules cannot apply, got %q", note)
	}
}

// TestPolicyShowReportsWhichRemoteItRead covers the "which origin?" question. Policy
// for a path resolves through a remote URL and "origin" is only a default, so a caller
// who disagrees with the verdict has to be able to see which remote was consulted.
func TestPolicyShowReportsWhichRemoteItRead(t *testing.T) {
	repo := realRepoWithRemote(t, "upstream", "https://abc.com/org/api.git")

	out, err := doPolicyShow(testWidgetCtx(map[string]string{},
		map[string]string{"repo": repo, "remote": "upstream"}))
	if err != nil {
		t.Fatalf("doPolicyShow: %v", err)
	}
	res := out.(map[string]any)
	rem, ok := res["remote"].(map[string]any)
	if !ok {
		t.Fatalf("the response must say which remote was read: %v", res)
	}
	if rem["name"] != "upstream" {
		t.Errorf("remote.name = %v, want upstream", rem["name"])
	}
	if res["repo_slug"] != "abc.com/org/api" {
		t.Errorf("repo_slug = %v, want it derived from that remote's URL", res["repo_slug"])
	}
	if res["resolved_from"] != "path" {
		t.Errorf("resolved_from = %v, want path", res["resolved_from"])
	}
}

// TestPolicyShowRemoteURLIsStripped guards the one way this operation could leak: a
// remote URL with an embedded credential is read straight out of .git/config, and the
// response echoes it.
func TestPolicyShowRemoteURLIsStripped(t *testing.T) {
	repo := realRepoWithRemote(t, "origin", "https://user:s3cr3t-token@abc.com/org/api.git")

	out, err := doPolicyShow(testWidgetCtx(map[string]string{}, map[string]string{"repo": repo}))
	if err != nil {
		t.Fatalf("doPolicyShow: %v", err)
	}
	rem, _ := out.(map[string]any)["remote"].(map[string]any)
	url, _ := rem["url"].(string)
	if strings.Contains(url, "s3cr3t-token") {
		t.Errorf("the reported remote URL still carries the credential: %q", url)
	}
	if !strings.Contains(url, "abc.com/org/api") {
		t.Errorf("stripping must keep the host and path, got %q", url)
	}
}

func containsExact(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}

// realRepo initialises an actual git repository. Unlike tempRepo, these tests read a
// remote URL, which means running git — a fake .git directory would make
// `git remote get-url` fail and turn every case into the no-remote case.
func realRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	return dir
}

func realRepoWithRemote(t *testing.T, name, url string) string {
	t.Helper()
	dir := realRepo(t)
	gitIn(t, dir, "remote", "add", name, url)
	return dir
}

// gitIn runs git in dir, resolved through ResolveGit and spawned through safeexec.
//
// pkg/safeexec's scanner enforces that across the repo, tests included, and the reason
// holds here as much as anywhere: Go's own LookPath calls faccessat2(2), which
// Android/Termux seccomp rejects with SIGSYS on kernels before 5.8. Exempting test files
// would also make the rule unenforceable in practice — a helper like this one is exactly
// what the next production callsite gets copied from.
//
// Resolving the path first (rather than passing "git" to safeexec.Command) is what gives
// these tests a clean skip on a machine with no git, instead of a failure inside the
// first command.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	gitPath, err := ResolveGit()
	if err != nil {
		t.Skip("git is not on PATH")
	}
	cmd := safeexec.Command(gitPath, args...)
	cmd.Dir = dir
	if out, cerr := cmd.CombinedOutput(); cerr != nil {
		t.Fatalf("git %v: %v\n%s", args, cerr, out)
	}
}
