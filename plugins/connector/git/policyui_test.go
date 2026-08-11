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
		`#pmw-t-fallback:checked~.pmw-cols #pmw-p-fallback{display:block}`,
		`#pmw-t-r0:checked~.pmw-cols #pmw-p-r0{display:block}`,
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
