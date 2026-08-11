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
	if !strings.Contains(strings.ToLower(html), "cleared") {
		t.Errorf(`a "-" column must be labelled as clearing the inherited value:\n%s`, html)
	}
	// The operator must never have to type "-" — the widget offers it.
	if !strings.Contains(html, "Clear") && !strings.Contains(html, "clear") {
		t.Errorf("the widget must offer a way to clear without typing a marker:\n%s", html)
	}
}

// TestGlobalShownFirstAsFallback asserts the layout puts GLOBAL above the
// overrides and says what it is for, so precedence reads off the page.
func TestFallbackIsTheFirstScopeInTheList(t *testing.T) {
	c := testCtx(map[string]string{
		"branch_name_pattern": `^ops/.+$`,
		"protected_branches":  `[{"branch":"master"},{"branch":"release/*"}]`,
		"allow_force_push":    "true",
		"repo_policies":       `[{"repo":"*/org/infra","branch_pattern":"^x/.+$"}]`,
	})
	html := renderPolicyManager(c, nil)

	// Precedence is expressed by list order now, not by vertical stacking: Fallback
	// is the first entry and everything below it wins over it.
	fi := strings.Index(html, `for="pmw-t-fallback"`)
	ri := strings.Index(html, `for="pmw-t-r0"`)
	if fi < 0 || ri < 0 {
		t.Fatalf("expected a Fallback entry and at least one override entry")
	}
	if fi > ri {
		t.Error("Fallback must be the first entry in the scope list")
	}

	// Every scope needs a panel AND a rule that reveals it. A missing pair is the
	// failure that shipped twice before: the tab highlights, the panel stays hidden.
	for _, want := range []string{
		`id="pmw-p-fallback"`,
		`id="pmw-p-r0"`,
		`id="pmw-p-sim"`,
		`#pmw-t-fallback:checked~.pmw-cols #pmw-p-fallback{display:block}`,
		`#pmw-t-r0:checked~.pmw-cols #pmw-p-r0{display:block}`,
		// The simulator is a scope in the same list, so it needs the same pair.
		`#pmw-t-sim:checked~.pmw-cols #pmw-p-sim{display:block}`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %q", want)
		}
	}

	// The stored values come back as editable inputs — this widget is their only
	// editor now.
	for _, want := range []string{
		`name="g_branch" value="^ops/.+$"`,
		`name="g_protected" value="master, release/*"`,
		`name="g_force" value="true" checked`,
		`data-op="policy_global_save"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the fallback editor is missing %q", want)
		}
	}
	assertNoTailwind(t, html)
}

// TestRuleShowsMatchingReposAndSpecificity covers requirement 2: which repos a
// glob matches, and how specific it is, so it is obvious which rule wins.
func TestRuleShowsMatchingReposAndSpecificity(t *testing.T) {
	c := testCtx(map[string]string{
		"repo_policies": `[{"repo":"*/org/*","branch_pattern":"^ops/.+$"},` +
			`{"repo":"*/org/infra","branch_pattern":"^infra/.+$"}]`,
	})
	html := renderPolicyManagerWithSamples(c, nil,
		[]string{"abc.com/org/infra", "abc.com/org/api", "example.com/other/thing"})

	// How specific a glob is must be on screen, but said in words rather than as a
	// wildcard count: "2 wildcards" is the implementation of the precedence rule,
	// not the thing the operator needs to know, which is which rule wins.
	if !strings.Contains(html, "matches broadly") {
		t.Errorf("expected */org/* named as the least specific scope:\n%s", html)
	}
	if !strings.Contains(html, "matches a group") {
		t.Errorf("expected */org/infra named as the more specific scope:\n%s", html)
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

// TestSimulateOpensTheSimulatorScope guards the one thing that breaks when the
// simulator lives inside the scope list rather than below it: the verdict is
// rendered into a panel that CSS hides unless its radio is checked, so a render
// that defaulted to Fallback would return the answer and hide it at the same time.
func TestSimulateOpensTheSimulatorScope(t *testing.T) {
	c := testWidgetCtx(map[string]string{"branch_name_pattern": `^fix/.+$`},
		map[string]string{"sim_repo": "abc.com/org/api", "sim_op": "push", "sim_branch": "fix/x"})

	out, err := doPolicySimulate(c)
	if err != nil {
		t.Fatalf("doPolicySimulate: %v", err)
	}
	html := out.(map[string]any)["html"].(string)

	if !strings.Contains(html, `id="pmw-t-sim" checked`) {
		t.Errorf("Simulate must leave the Simulator scope selected:\n%s", html)
	}
	if strings.Contains(html, `id="pmw-t-fallback" checked`) {
		t.Error("Simulate must not leave the Fallback scope selected — it hides the verdict")
	}
	// And the verdict has to be inside that panel, not elsewhere on the page.
	panel := html[strings.Index(html, `id="pmw-p-sim"`):]
	if !strings.Contains(panel, "ALLOWED") {
		t.Errorf("the verdict must render inside the simulator panel:\n%s", panel)
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
// assertNoTailwind checks that every class the markup uses is one this widget
// defines itself.
//
// The rule is not "no class attributes" — that was the old shape, when the widget
// styled everything inline. A widget that ships its own <style> block may use class
// names freely, as long as they are its own: what Tailwind's build purges is classes
// it generated, and it never scans markup a connector returns at runtime. An
// unprefixed name is the real hazard, because it would also restyle the manager's
// own page around the widget.
func assertNoTailwind(t *testing.T, html string) {
	t.Helper()
	for _, frag := range strings.Split(html, `class="`)[1:] {
		i := strings.IndexByte(frag, '"')
		if i < 0 {
			t.Errorf("unterminated class attribute:\n%s", html)
			continue
		}
		for _, cls := range strings.Fields(frag[:i]) {
			if !strings.HasPrefix(cls, "pmw-") {
				t.Errorf("class %q is not defined by this widget; it would leak or be purged", cls)
			}
		}
	}
}

func TestPolicyGlobalSaveWritesEveryField(t *testing.T) {
	// The config fields are hidden, so this op is the only way they get set. If it
	// drops one, that rule becomes unreachable with no error anywhere.
	c := testWidgetCtx(map[string]string{}, map[string]string{
		"g_branch":    `^(fix|feat)/.+$`,
		"g_message":   `^(feat|fix): .+`,
		"g_protected": "main, master, release/*",
		"g_force":     "true",
	})
	out, err := doPolicyGlobalSave(c)
	if err != nil {
		t.Fatalf("doPolicyGlobalSave: %v", err)
	}
	m := out.(map[string]any)
	fields, ok := m["fields"].(map[string]string)
	if !ok {
		t.Fatalf("no fields map returned: %v", m["fields"])
	}

	if fields["branch_name_pattern"] != `^(fix|feat)/.+$` {
		t.Errorf("branch_name_pattern = %q", fields["branch_name_pattern"])
	}
	if fields["commit_message_pattern"] != `^(feat|fix): .+` {
		t.Errorf("commit_message_pattern = %q", fields["commit_message_pattern"])
	}
	if fields["allow_force_push"] != "true" {
		t.Errorf("allow_force_push = %q, want true", fields["allow_force_push"])
	}
	// protected_branches is a kvlist, so it must be the JSON array shape the parser
	// reads — not the comma-separated text the operator typed.
	rows, perr := ParseKVList(fields["protected_branches"])
	if perr != nil {
		t.Fatalf("protected_branches is not valid kvlist JSON: %v", perr)
	}
	if len(rows) != 3 || rows[0]["branch"] != "main" || rows[2]["branch"] != "release/*" {
		t.Errorf("protected_branches = %q, want three branch rows", fields["protected_branches"])
	}

	// The re-render must show what was just submitted; reading config here would
	// redraw the old values and look like the save was ignored.
	html, _ := m["html"].(string)
	if !strings.Contains(html, `value="^(fix|feat)/.+$"`) {
		t.Errorf("the re-render does not show the values just saved:\n%s", html)
	}
}

func TestPolicyGlobalSaveRefusesAnUncompilableRegex(t *testing.T) {
	// A pattern that does not compile blocks every mutating operation at runtime, so
	// storing one would take the connector offline with nothing on screen explaining
	// why. Refuse the save and keep the working policy in force.
	for _, tc := range []struct{ field, value, wantName string }{
		{"g_branch", `^(fix/.+$`, "Branch name pattern"},
		{"g_message", `^(feat: .+`, "Commit message pattern"},
	} {
		in := map[string]string{"g_branch": "", "g_message": "", "g_protected": ""}
		in[tc.field] = tc.value

		out, err := doPolicyGlobalSave(testWidgetCtx(map[string]string{}, in))
		if err != nil {
			t.Fatalf("%s: %v", tc.field, err)
		}
		m := out.(map[string]any)
		if _, wrote := m["fields"]; wrote {
			t.Errorf("%s: a malformed regex was stored", tc.field)
		}
		html, _ := m["html"].(string)
		if !strings.Contains(html, tc.wantName+" was not saved") {
			t.Errorf("%s: the refusal does not name the field:\n%s", tc.field, html)
		}
	}
}

func TestPolicyGlobalSaveTreatsAnAbsentCheckboxAsFalse(t *testing.T) {
	// A browser sends nothing for an unchecked box, so absence has to mean denied.
	// Reading it as "unset, keep the old value" would make force push impossible to
	// turn back off.
	out, err := doPolicyGlobalSave(testWidgetCtx(
		map[string]string{"allow_force_push": "true"},
		map[string]string{"g_branch": "", "g_message": "", "g_protected": ""}, // no g_force
	))
	if err != nil {
		t.Fatalf("doPolicyGlobalSave: %v", err)
	}
	fields := out.(map[string]any)["fields"].(map[string]string)
	if fields["allow_force_push"] != "false" {
		t.Errorf("allow_force_push = %q, want false when the box is unchecked",
			fields["allow_force_push"])
	}
}

func TestSimulatorJudgesACommitMessage(t *testing.T) {
	cfg := map[string]string{"commit_message_pattern": `^(feat|fix): .+`}

	deny, err := doPolicySimulate(testWidgetCtx(cfg, map[string]string{
		"sim_op": "commit", "sim_branch": "fix/x", "sim_message": "wip",
	}))
	if err != nil {
		t.Fatalf("simulate: %v", err)
	}
	if html, _ := deny.(map[string]any)["html"].(string); !strings.Contains(html, "DENIED") {
		t.Errorf("a message violating the pattern was allowed:\n%s", html)
	}

	allow, err := doPolicySimulate(testWidgetCtx(cfg, map[string]string{
		"sim_op": "commit", "sim_branch": "fix/x", "sim_message": "fix: real change",
	}))
	if err != nil {
		t.Fatalf("simulate: %v", err)
	}
	html, _ := allow.(map[string]any)["html"].(string)
	if !strings.Contains(html, "ALLOWED") {
		t.Errorf("a conforming message was refused:\n%s", html)
	}
	// The message and the pattern in force both belong in the verdict, so a denial is
	// explainable without going back to the fields.
	if !strings.Contains(html, "fix: real change") {
		t.Errorf("the verdict does not echo the message judged:\n%s", html)
	}
	if !strings.Contains(html, `^(feat|fix): .+`) {
		t.Errorf("the verdict does not report the pattern in force:\n%s", html)
	}
}

func TestPolicyWidgetOwnsEveryPolicyField(t *testing.T) {
	// The premise of the single-widget design: no policy field may render as an
	// ordinary row, or the same value would be editable in two places with nothing
	// saying which wins.
	owned := map[string]bool{
		"branch_name_pattern":    true,
		"commit_message_pattern": true,
		"protected_branches":     true,
		"allow_force_push":       true,
		"repo_policies":          true,
	}
	for _, cfg := range entity.StructToConfigs(Config{}) {
		if !owned[cfg.Key] {
			continue
		}
		if !cfg.Hidden {
			t.Errorf("config %q is not hidden; the widget is meant to be its only editor", cfg.Key)
		}
		if cfg.Group != "" {
			t.Errorf("config %q carries group %q; a hidden field should not claim a card", cfg.Key, cfg.Group)
		}
	}
}

// TestControlsDoNotShareTheirContainersBackground is a regression guard for a bug
// that shipped twice, in both columns: a bordered control drawn on the same
// background as the box holding it. The border is a theme token one step from the
// background, so at that point it visually vanishes and an input stops reading as
// something you can type in — which is exactly how it was reported ("that doesn't
// look like a button or a menu").
//
// The rule the widget follows: containers are recessed (uiSunken), controls sit on
// top (uiPanel). Asserting it here rather than by eye because the CSS is generated
// by index into one format string, so a token can move without any test noticing.
func TestControlsDoNotShareTheirContainersBackground(t *testing.T) {
	css := policyStyle("pmw", 1)

	bg := func(sel string) string {
		i := strings.Index(css, sel+"{")
		if i < 0 {
			t.Fatalf("selector %s missing from the stylesheet", sel)
		}
		rule := css[i:]
		rule = rule[:strings.Index(rule, "}")]
		j := strings.LastIndex(rule, "background:")
		if j < 0 {
			return ""
		}
		v := rule[j+len("background:"):]
		if k := strings.Index(v, ";"); k >= 0 {
			v = v[:k]
		}
		return v
	}

	for _, c := range []struct{ container, control string }{
		{".pmw-list", ".pmw-in"},   // the scope rail and the new-repo input
		{".pmw-list", ".pmw-btn2"}, // ...and the Add repository button
		{".pmw-panel", ".pmw-in"},  // the detail panel and its fields
	} {
		cb, kb := bg(c.container), bg(c.control)
		if cb == "" || kb == "" {
			t.Errorf("%s or %s has no background; both need one to be distinguishable", c.container, c.control)
			continue
		}
		if cb == kb {
			t.Errorf("%s sits on %s but both are %s — its border melts into the box", c.control, c.container, cb)
		}
	}
}

// TestRulePanelEditsTheMessagePattern locks the per-repo commit rule into the
// widget: the field renders, and a Save round-trips it into storage. Without the
// second half the column would exist in the engine but be unreachable from the UI,
// which is where it was when the operator asked for it.
func TestRulePanelEditsTheMessagePattern(t *testing.T) {
	cfg := map[string]string{
		"repo_policies": `[{"repo":"*/org/tickets","message_pattern":"^[A-Z]+-[0-9]+ .+"}]`,
	}
	html := renderPolicyManager(testCtx(cfg), nil)

	if !strings.Contains(html, `name="r_message_0" value="^[A-Z]+-[0-9]+ .+"`) {
		t.Errorf("the stored per-repo message pattern must render as an editable input:\n%s", html)
	}

	out, err := doPolicyRuleUpdate(testWidgetCtx(cfg, map[string]string{
		"browser":       "0",
		"r_repo_0":      "*/org/tickets",
		"r_message_0":   `^(feat|fix)\(.+\): .+`,
		"r_branch_0":    "",
		"r_protected_0": "",
		"r_force_0":     "",
	}))
	if err != nil {
		t.Fatalf("doPolicyRuleUpdate: %v", err)
	}
	stored := out.(map[string]any)["fields"].(map[string]string)["repo_policies"]
	rules, err := ParseRepoRules(stored)
	if err != nil {
		t.Fatalf("ParseRepoRules: %v", err)
	}
	if len(rules) != 1 || rules[0].MessagePattern != `^(feat|fix)\(.+\): .+` {
		t.Errorf("the saved rule lost its message pattern: %+v", rules)
	}
}

// TestRuleUpdateRefusesAnUncompilableMessagePattern mirrors the branch-pattern
// guard. Either column is fail-closed in Resolve, so storing a broken one takes
// every mutation offline — refusing at save time is the only place it is cheap.
func TestRuleUpdateRefusesAnUncompilableMessagePattern(t *testing.T) {
	cfg := map[string]string{"repo_policies": `[{"repo":"*/org/x"}]`}
	out, err := doPolicyRuleUpdate(testWidgetCtx(cfg, map[string]string{
		"browser": "0", "r_repo_0": "*/org/x", "r_message_0": `^(fix`,
	}))
	if err != nil {
		t.Fatalf("doPolicyRuleUpdate: %v", err)
	}
	m := out.(map[string]any)
	if _, wrote := m["fields"]; wrote {
		t.Error("a pattern that does not compile must not be stored")
	}
	if !strings.Contains(m["html"].(string), "commit message pattern does not compile") {
		t.Errorf("the refusal must name which pattern failed:\n%s", m["html"])
	}
}

// TestClearInheritedCoversEveryInheritableColumn guards the Clear button against
// the drift that a new column introduces: adding one to the panel without adding it
// here leaves it inheriting after a click that claims to have cleared everything.
func TestClearInheritedCoversEveryInheritableColumn(t *testing.T) {
	cfg := map[string]string{
		"repo_policies": `[{"repo":"*/org/x","branch_pattern":"^a/.+$","message_pattern":"^b: .+","protected":"main"}]`,
	}
	out, err := doPolicyRuleClear(testWidgetCtx(cfg, map[string]string{"browser": "0"}))
	if err != nil {
		t.Fatalf("doPolicyRuleClear: %v", err)
	}
	rules, err := ParseRepoRules(out.(map[string]any)["fields"].(map[string]string)["repo_policies"])
	if err != nil {
		t.Fatalf("ParseRepoRules: %v", err)
	}
	r := rules[0]
	for _, c := range []struct{ name, got string }{
		{"branch_pattern", r.BranchPattern},
		{"message_pattern", r.MessagePattern},
		{"protected", r.Protected},
	} {
		if c.got != "-" {
			t.Errorf("%s = %q, want the cleared marker", c.name, c.got)
		}
	}
}

// TestFieldsNameTheirSyntax is the "how do I fill this in" guard. A text box cannot
// say whether it wants a regex or a glob, and getting it wrong is silent: a glob
// typed into a regex field compiles and then matches almost nothing. Every pattern
// field must name RE2 and every glob field must say glob, in both panels, and the
// placeholder has to be a working example rather than a description of the shape.
func TestFieldsNameTheirSyntax(t *testing.T) {
	// Empty everywhere: the help lines have no value to infer the syntax from, which
	// is exactly when the operator needs to be told.
	html := renderPolicyManager(testCtx(map[string]string{
		"repo_policies": `[{"repo":"*/org/x"}]`,
	}), nil)

	for _, want := range []string{
		"RE2 regex",              // the pattern fields name their language
		"^…$",                    // ...and warn that it is unanchored
		"* is the only wildcard", // the glob fields distinguish themselves
		esc(branchPlaceholder),   // real examples, not "enter a pattern"
		esc(messagePlaceholder),
		protectedPlaceholder,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the widget never tells the operator %q:\n%s", want, html)
		}
	}

	// Both panels, at EVERY state. The override's help used to state only which layer
	// wins, so the syntax went missing in the one panel where a new pattern is
	// actually typed — and it went missing precisely when the field was empty and the
	// operator had nothing to infer it from.
	for _, panel := range []struct{ name, body string }{
		{"fallback", panelOf(t, html, "fallback")},
		{"override", panelOf(t, html, "r0")},
	} {
		for _, want := range []string{
			esc(branchSpec.label), esc(branchSpec.placeholder),
			esc(messageSpec.label), esc(messageSpec.placeholder),
			esc(protectedSpec.label), esc(protectedSpec.placeholder),
			esc(reSyntax), esc(globSyntax),
		} {
			if !strings.Contains(panel.body, want) {
				t.Errorf("the %s panel is missing %q:\n%s", panel.name, want, panel.body)
			}
		}
	}
}

// panelOf slices out one scope's panel so an assertion can name which panel it is
// about. Asserting against the whole widget would pass whenever ANY panel carried
// the text, which is how the override panel's missing help went unnoticed.
func panelOf(t *testing.T, html, scope string) string {
	t.Helper()
	open := `id="pmw-p-` + scope + `"`
	i := strings.Index(html, open)
	if i < 0 {
		t.Fatalf("panel %s not rendered", scope)
	}
	rest := html[i:]
	// Panels are siblings, so the next panel's id ends this one.
	if j := strings.Index(rest[len(open):], `id="pmw-p-`); j >= 0 {
		rest = rest[:len(open)+j]
	}
	return rest
}

// TestBothPanelsShareOneWording is the guard for the drift itself: every field is
// described by one spec, so the label, the gating sentence and the syntax sentence
// are the same string in both panels. Two code paths building two vocabularies for
// one field is what produced "RE2 regex, unanchored" on one side and "replaces the
// fallback's branch pattern" on the other.
func TestBothPanelsShareOneWording(t *testing.T) {
	html := renderPolicyManager(testCtx(map[string]string{
		"branch_name_pattern":    `^fix/.+$`,
		"commit_message_pattern": `^fix: .+`,
		"protected_branches":     `[{"branch":"main"}]`,
		"repo_policies":          `[{"repo":"*/org/x","branch_pattern":"^ops/.+$","message_pattern":"^ops: .+","protected":"master"}]`,
	}), nil)

	fallback := panelOf(t, html, "fallback")
	rule := panelOf(t, html, "r0")

	// With BOTH panels filled in, the gating sentence and the syntax sentence must
	// match verbatim — only the inherit/override sentence may differ.
	for _, sp := range []fieldSpec{branchSpec, messageSpec, protectedSpec} {
		for _, want := range []string{esc(sp.label), esc(sp.gates), esc(sp.syntax)} {
			if !strings.Contains(fallback, want) {
				t.Errorf("fallback panel is missing %q", want)
			}
			if !strings.Contains(rule, want) {
				t.Errorf("override panel is missing %q", want)
			}
		}
	}

	// And the one sentence that SHOULD differ does: only the override talks about
	// layering. Without this the test would also pass if the two panels collapsed
	// into one identical block, losing the thing an override has to explain.
	if !strings.Contains(rule, "Overrides the fallback") {
		t.Error("the override panel must say it overrides the fallback")
	}
	if strings.Contains(fallback, "Overrides the fallback") {
		t.Error("the fallback panel must not claim to override anything")
	}
}
