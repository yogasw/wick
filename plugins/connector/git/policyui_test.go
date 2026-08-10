package main

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/entity"
)

// testWidgetCtx builds a Ctx carrying both config and the named form inputs the
// html widget posts back. testCtx (service_test.go) covers the config-only case.
func testWidgetCtx(cfg, input map[string]string) *connector.Ctx {
	return connector.NewPluginCtx(context.Background(), cfg, input)
}

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
		Repo: "abc.com/org/infra", Op: "push", Branch: "fix/login-bug",
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
	assertNoTailwind(t, html)
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
		Repo: "abc.com/org/api", Op: "push", Branch: "fix/login-bug",
		V: v, Pol: pol, Command: "git push origin fix/login-bug",
	})
	if !strings.Contains(html, "ALLOWED") {
		t.Errorf("expected ALLOWED in the output:\n%s", html)
	}
	if !strings.Contains(html, "global") {
		t.Errorf("expected the matched rule reported:\n%s", html)
	}
}

// TestEscapesUserInput covers both HTML contexts a value can land in. Element
// text is the obvious one; the attribute context matters just as much because
// the simulator form echoes what the operator typed back into value="…", where a
// bare quote would close the attribute and everything after it becomes markup.
func TestEscapesUserInput(t *testing.T) {
	const payload = `<script>alert(1)</script>`

	html := renderSimulation(simResult{
		Repo: "x", Op: "push", Branch: payload,
		V: Verdict{Allow: false, Reason: "nope", MatchedRule: "global"},
	})
	if strings.Contains(html, "<script>") {
		t.Errorf("unescaped user input in the simulation output:\n%s", html)
	}

	// Attribute context: the branch is echoed into the form's value="…".
	const attrPayload = `" onmouseover="alert(1)`
	c := testCtx(map[string]string{})
	widget := renderPolicyManager(c, &simResult{
		Repo: attrPayload, Op: "push", Branch: payload,
		V: Verdict{Allow: true, MatchedRule: "global"},
	})
	// The payload must survive only as inert text: its quotes have to be encoded,
	// otherwise it would close value="…" and the rest becomes a live attribute.
	// Asserting on the encoded form is what proves it, since the raw substring
	// onmouseover= legitimately appears inside the escaped text.
	if !strings.Contains(widget, `value="&#34; onmouseover=&#34;alert(1)"`) {
		t.Errorf("the quotes in an attribute value must be encoded:\n%s", widget)
	}
	if strings.Contains(widget, `" onmouseover="alert(1)`) {
		t.Errorf("attribute-context injection escaped the encoder:\n%s", widget)
	}
	if strings.Contains(widget, "<script>") {
		t.Errorf("unescaped user input in the widget output:\n%s", widget)
	}
}

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

	if warn := validateRule(RepoRule{Repo: "*/org/x", ForcePush: "yes"}); warn == "" {
		t.Error("expected a warning for a force_push value that is not true/false/inherit")
	}
}

// TestInheritAndClearAreLabelled is the reason the widget exists: in a raw
// kvlist, "" and "-" are two indistinguishable-looking blanks with opposite
// meanings. The widget must spell both out.
func TestInheritAndClearAreLabelled(t *testing.T) {
	c := testCtx(map[string]string{
		"branch_name_pattern": `^(fix|feat)/.+$`,
		"protected_branches":  `[{"branch":"master"}]`,
		"repo_policies": `[{"repo":"*/org/inherits","branch_pattern":"","protected":"","force_push":""},` +
			`{"repo":"*/org/clears","branch_pattern":"-","protected":"-","force_push":"-"}]`,
	})
	html := renderPolicyManager(c, nil)

	if !strings.Contains(html, "inherit") {
		t.Errorf("an empty column must be labelled as inheriting from global:\n%s", html)
	}
	if !strings.Contains(html, "cleared") {
		t.Errorf(`a "-" column must be labelled as clearing the inherited value:\n%s`, html)
	}
	// The operator must never have to type "-" — the widget offers it.
	if !strings.Contains(html, "Clear") && !strings.Contains(html, "clear") {
		t.Errorf("the widget must offer a way to clear without typing a marker:\n%s", html)
	}
}

// TestGlobalShownFirstAsFallback asserts the layout puts GLOBAL above the
// overrides and says what it is for, so precedence reads off the page.
func TestGlobalShownFirstAsFallback(t *testing.T) {
	c := testCtx(map[string]string{
		"branch_name_pattern": `^ops/.+$`,
		"protected_branches":  `[{"branch":"master"},{"branch":"release/*"}]`,
		"allow_force_push":    "true",
		"repo_policies":       `[{"repo":"*/org/infra","branch_pattern":"^x/.+$"}]`,
	})
	html := renderPolicyManager(c, nil)

	gi := strings.Index(html, "GLOBAL")
	ri := strings.Index(html, "PER-REPO")
	if gi < 0 || ri < 0 {
		t.Fatalf("expected both a GLOBAL and a PER-REPO section:\n%s", html)
	}
	if gi > ri {
		t.Error("GLOBAL must render before the per-repo overrides so it reads as the fallback")
	}
	for _, want := range []string{`^ops/.+$`, "release/*", "allowed"} {
		if !strings.Contains(html, want) {
			t.Errorf("global summary is missing %q\n%s", want, html)
		}
	}
	assertNoTailwind(t, html)
}

// TestRuleShowsMatchingReposAndWildcards covers requirement 2: which repos a
// glob matches, and how specific it is, so it is obvious which rule wins.
func TestRuleShowsMatchingReposAndWildcards(t *testing.T) {
	c := testCtx(map[string]string{
		"repo_policies": `[{"repo":"*/org/*","branch_pattern":"^ops/.+$"},` +
			`{"repo":"*/org/infra","branch_pattern":"^infra/.+$"}]`,
	})
	html := renderPolicyManagerWithSamples(c, nil,
		[]string{"abc.com/org/infra", "abc.com/org/api", "example.com/other/thing"})

	if !strings.Contains(html, "2 wildcard") {
		t.Errorf("expected the wildcard count of */org/* reported:\n%s", html)
	}
	if !strings.Contains(html, "abc.com/org/api") {
		t.Errorf("expected the repos a glob matches to be listed:\n%s", html)
	}
	if strings.Contains(html, "example.com/other/thing") {
		t.Error("a repo that does not match any glob must not be listed under a rule")
	}
}

// TestSaveRulesReturnsFieldsAndKeepsWidget locks the two halves of the editor
// round-trip in place: the new value must come back under {fields} keyed by the
// real config key, and the returned {html} must still be the whole widget.
//
// HtmlField.svelte replaces the widget body with whatever html an op returns and
// does NOT re-fetch afterwards, so returning only a one-line notice would delete
// the editor and simulator from the page until the operator reloaded it.
func TestSaveRulesReturnsFieldsAndKeepsWidget(t *testing.T) {
	c := testWidgetCtx(map[string]string{"repo_policies": "[]"}, map[string]string{
		"rule_json": `[{"repo":"*/org/infra","branch_pattern":"^ops/.+$","protected":"master","force_push":"false"}]`,
	})

	out, err := doPolicyRuleSave(c)
	if err != nil {
		t.Fatalf("doPolicyRuleSave: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("doPolicyRuleSave returned %T, want map[string]any", out)
	}

	fields, ok := m["fields"].(map[string]string)
	if !ok {
		t.Fatalf("fields is %T, want map[string]string", m["fields"])
	}
	saved, ok := fields["repo_policies"]
	if !ok {
		t.Fatalf("fields must be keyed by the repo_policies config key, got %v", fields)
	}
	// The value the core will persist must parse back to the same rule.
	back, perr := ParseRepoRules(saved)
	if perr != nil {
		t.Fatalf("the saved value does not parse back: %v", perr)
	}
	if len(back) != 1 || back[0].BranchPattern != `^ops/.+$` {
		t.Errorf("round-trip lost data: %+v", back)
	}
	// repo_policies must be a real config key, or the core silently drops the write.
	if !configKeySet(t)["repo_policies"] {
		t.Error("repo_policies is not a declared Config field, so {fields} would be ignored")
	}

	widget, ok := m["html"].(string)
	if !ok {
		t.Fatalf("html is %T, want string", m["html"])
	}
	for _, want := range []string{"Saved", `name="rule_json"`, `data-op="policy_simulate"`} {
		if !strings.Contains(widget, want) {
			t.Errorf("the save response must re-render the whole widget; missing %q\n%s", want, widget)
		}
	}
	// The re-render must show the rules just saved, not the stale stored value.
	if !strings.Contains(widget, "*/org/infra") {
		t.Errorf("the re-render must reflect the rules just saved:\n%s", widget)
	}
}

// TestSaveRulesRejectsBadJSONWithoutWriting makes sure a malformed paste cannot
// overwrite a working rule set.
func TestSaveRulesRejectsBadJSONWithoutWriting(t *testing.T) {
	c := testWidgetCtx(map[string]string{"repo_policies": `[{"repo":"*/org/keep"}]`},
		map[string]string{"rule_json": `{not json`})

	out, err := doPolicyRuleSave(c)
	if err != nil {
		t.Fatalf("doPolicyRuleSave: %v", err)
	}
	m := out.(map[string]any)
	if _, present := m["fields"]; present {
		t.Error("malformed input must not write anything to config")
	}
	html, _ := m["html"].(string)
	if !strings.Contains(html, "Could not save") {
		t.Errorf("expected a failure notice explaining the parse error:\n%s", html)
	}
	// The existing rule must still be on screen, not wiped by the failed save.
	if !strings.Contains(html, "*/org/keep") {
		t.Errorf("a failed save must leave the stored rules visible:\n%s", html)
	}
}

// TestSimulatorUsesTheRealPolicyPath is the load-bearing test for the whole
// widget: the simulator must agree with the operations. It drives
// doPolicySimulate through a Ctx and compares the verdict against policyFor +
// Evaluate — the exact path execute() takes.
func TestSimulatorUsesTheRealPolicyPath(t *testing.T) {
	cfg := map[string]string{
		"branch_name_pattern": `^(fix|feat)/.+$`,
		"protected_branches":  `[{"branch":"master"},{"branch":"release/*"}]`,
		"repo_policies":       `[{"repo":"*/org/infra","branch_pattern":"^ops/.+$","protected":"-"}]`,
	}

	cases := []struct {
		name   string
		repo   string
		op     string
		branch string
	}{
		{"per-repo pattern denies a fix branch", "abc.com/org/infra", "branch_create", "fix/login"},
		{"per-repo pattern allows an ops branch", "abc.com/org/infra", "branch_create", "ops/rotate"},
		{"per-repo clear unprotects master", "abc.com/org/infra", "push", "master"},
		{"global protects master elsewhere", "abc.com/org/api", "push", "master"},
		{"global pattern allows a feat branch", "abc.com/org/api", "branch_create", "feat/x"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := testWidgetCtx(cfg, map[string]string{
				"sim_repo": tc.repo, "sim_op": tc.op, "sim_branch": tc.branch,
			})
			out, err := doPolicySimulate(c)
			if err != nil {
				t.Fatalf("doPolicySimulate: %v", err)
			}
			html := out.(map[string]any)["html"].(string)

			// The oracle: the same compiler and evaluator the operations use.
			want := policyFor(testCtx(cfg), tc.repo, tc.repo).
				Evaluate(Request{Op: tc.op, Branch: tc.branch, NewBranch: tc.op == "branch_create"})

			badge := "DENIED"
			if want.Allow {
				badge = "ALLOWED"
			}
			if !strings.Contains(html, badge) {
				t.Errorf("simulator disagrees with the real policy path: want %s\n%s", badge, html)
			}
			if !strings.Contains(html, want.MatchedRule) {
				t.Errorf("simulator must report the deciding rule %q\n%s", want.MatchedRule, html)
			}
		})
	}
}

// TestSimulateReportsInvalidStoredRules makes sure a broken repo_policies value
// surfaces in the simulator instead of silently resolving to the global policy.
func TestSimulateReportsInvalidStoredRules(t *testing.T) {
	c := testWidgetCtx(map[string]string{"repo_policies": `[{"repo":`},
		map[string]string{"sim_repo": "abc.com/org/api", "sim_op": "push", "sim_branch": "fix/x"})

	out, err := doPolicySimulate(c)
	if err != nil {
		t.Fatalf("doPolicySimulate: %v", err)
	}
	html := out.(map[string]any)["html"].(string)
	if !strings.Contains(html, "not valid JSON") {
		t.Errorf("a malformed repo_policies must be reported:\n%s", html)
	}
	// Malformed config blocks mutations, so a push must come back denied.
	if !strings.Contains(html, "DENIED") {
		t.Errorf("a mutation under a malformed policy must be denied:\n%s", html)
	}
}

func TestPolicyManagerRendersEmptyState(t *testing.T) {
	c := testCtx(map[string]string{})
	html := renderPolicyManager(c, nil)

	if !strings.Contains(html, `name="rule_json"`) {
		t.Errorf("the editor textarea must be present even with no rules:\n%s", html)
	}
	if !strings.Contains(html, `data-op="policy_rule_save"`) {
		t.Errorf("the save button must be present:\n%s", html)
	}
	if !strings.Contains(html, `data-op="policy_simulate"`) {
		t.Errorf("the simulate button must be present:\n%s", html)
	}
	assertNoTailwind(t, html)
	if !strings.Contains(html, "var(--color-") {
		t.Error("the widget must style with theme CSS variables")
	}
}

// TestPolicyManagerOpReturnsHTML checks the op contract itself: the html= widget
// reads the "html" key off the response, so anything else renders blank.
func TestPolicyManagerOpReturnsHTML(t *testing.T) {
	out, err := doPolicyManager(testCtx(map[string]string{}))
	if err != nil {
		t.Fatalf("doPolicyManager: %v", err)
	}
	m, ok := out.(map[string]any)
	if !ok {
		t.Fatalf("doPolicyManager returned %T, want map[string]any", out)
	}
	s, ok := m["html"].(string)
	if !ok || s == "" {
		t.Fatalf(`response must carry a non-empty "html" key, got %v`, m)
	}
}

// TestPolicyWidgetInputsAreDeclared guards the other half of the input contract:
// HtmlField sends every named form control as input.<name>, and c.Input only
// resolves keys the op's Input struct declares.
func TestPolicyWidgetInputsAreDeclared(t *testing.T) {
	configs := entity.StructToConfigs(policyManagerInput{})
	have := make(map[string]bool, len(configs))
	for _, cfg := range configs {
		have[cfg.Key] = true
	}
	for _, key := range []string{"browser", "sim_repo", "sim_op", "sim_branch", "rule_json"} {
		if !have[key] {
			t.Errorf("policyManagerInput does not declare %q, so c.Input(%q) is always empty", key, key)
		}
	}
}

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
				if op.Destructive {
					t.Errorf("op %q must not be destructive; it only renders and validates", op.Key)
				}
			}
		}
	}
	if found != 3 {
		t.Errorf("found %d policy widget ops, want 3", found)
	}
}

// TestPolicyManagerOpKeyMatchesConfigTag ties the widget's op key to the tag on
// the Config field. A rename on either side leaves the field rendering "Couldn't
// load: unknown operation".
func TestPolicyManagerOpKeyMatchesConfigTag(t *testing.T) {
	var opts string
	for _, cfg := range entity.StructToConfigs(Config{}) {
		if cfg.Key == "policy_manager" {
			opts = cfg.Options
		}
	}
	if opts == "" {
		t.Fatal("no config field declares html=policy_manager")
	}
	var registered bool
	for _, cat := range Operations() {
		for _, op := range cat.Ops {
			if op.Key == opts {
				registered = true
			}
		}
	}
	if !registered {
		t.Errorf("the html= widget points at op %q, which Operations() does not register", opts)
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

// assertNoTailwind fails when markup carries Tailwind utility classes. The
// manager's Tailwind build does not scan HTML a connector returns at runtime, so
// those classes are purged and the widget renders unstyled.
func assertNoTailwind(t *testing.T, html string) {
	t.Helper()
	if strings.Contains(html, `class="`) {
		t.Errorf("connector HTML must not use class= at all; Tailwind purges it:\n%s", html)
	}
}
