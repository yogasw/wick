// policyui.go implements the Policy Rules config widget: an editor for per-repo
// rules plus a simulator that answers "what would happen if" before anything is
// pushed.
//
// Why a widget instead of a plain kvlist. A per-repo rule mixes one regex
// (branch_pattern) with two glob columns (protected, repo) in narrow table cells
// with no validation, and it encodes inherit-vs-clear as the difference between
// an empty cell and a cell holding "-". Both failure modes are silent: a regex
// that does not compile, or a cell left blank when it should have been cleared,
// is only discovered later when a push is unexpectedly blocked or unexpectedly
// allowed. This widget makes all of it visible at edit time — it compiles every
// regex, names the repos each glob matches, spells out "inherit" and "cleared"
// in words, and offers a simulator.
//
// The simulator calls the same Resolve and Evaluate the operations use, through
// the same loadGlobal + ParseRepoRules pair, so it cannot drift from real
// behaviour. If it says ALLOWED, the operation is allowed. A second
// implementation would be worse than no simulator at all.
//
// All markup is styled with inline CSS variables rather than Tailwind classes:
// the manager's Tailwind build does not scan HTML a connector returns at
// runtime, so utility classes would be purged and the widget would render
// unstyled. Green (#27B199) is fixed across themes, so it is inlined literally.
package main

import (
	"encoding/json"
	"fmt"
	"html"
	"sort"
	"strconv"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// Theme tokens. Kept in one place so the whole widget shifts with the theme and
// a reviewer can see at a glance that no Tailwind class is involved.
const (
	uiBorder = "var(--color-navy-200)"
	uiPanel  = "var(--color-white-100)"
	uiSunken = "var(--color-white-200)"
	uiText   = "var(--color-black-900)"
	uiOK     = "#27B199"
	uiBad    = "var(--color-red-500, #E5484D)"
)

// simResult is one simulator run.
type simResult struct {
	Repo    string
	Op      string
	Branch  string
	Message string
	V       Verdict
	Pol     EffectivePolicy
	Command string
}

// esc escapes text destined for HTML. Every interpolated value goes through it —
// repo names, branch names, regexes and error strings are all operator input,
// and html.EscapeString covers the attribute context too because it escapes both
// quote characters, not just the angle brackets.
func esc(s string) string { return html.EscapeString(s) }

// renderSimulation renders the verdict panel.
func renderSimulation(s simResult) string {
	badge, badgeColor := "ALLOWED", uiOK
	if !s.V.Allow {
		badge, badgeColor = "DENIED", uiBad
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<div style="border:1px solid %s;border-radius:8px;padding:12px;margin-top:8px;background:%s;color:%s">`,
		uiBorder, uiPanel, uiText)
	fmt.Fprintf(&b, `<div style="font-weight:700;font-size:13px;color:%s;margin-bottom:8px">%s</div>`,
		badgeColor, badge)

	row := func(label, value string) { fmt.Fprint(&b, kvRow(label, value)) }

	if s.Repo != "" {
		row("Repository", s.Repo)
	}
	if s.Message != "" {
		row("Commit message", s.Message)
	}
	row("Matched rule", s.V.MatchedRule)
	if s.V.Reason != "" {
		row("Reason", s.V.Reason)
	}
	if s.Command != "" {
		row("Would run", s.Command)
		fmt.Fprintf(&b, `<div style="font-size:11px;opacity:.6;margin:2px 0 8px 118px">not executed</div>`)
	}

	fmt.Fprintf(&b, `<div style="border-top:1px solid %s;margin-top:8px;padding-top:8px">`, uiBorder)
	fmt.Fprintf(&b, `<div style="font-size:12px;font-weight:600;margin-bottom:4px">Effective rules for this repo</div>`)
	pattern := s.Pol.BranchPattern
	if pattern == "" {
		pattern = "(none — any branch name is accepted)"
	}
	row("branch pattern", pattern)
	msgPat := s.Pol.MessagePattern
	if msgPat == "" {
		msgPat = "(none — any commit message is accepted)"
	}
	row("commit message", msgPat)
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
	fmt.Fprint(&b, `</div></div>`)
	return b.String()
}

// kvRow is the label/value line used throughout the widget.
func kvRow(label, value string) string {
	return fmt.Sprintf(`<div style="display:flex;gap:8px;font-size:12px;margin-bottom:4px">`+
		`<div style="min-width:110px;opacity:.7">%s</div>`+
		`<div style="font-family:monospace;word-break:break-all">%s</div></div>`,
		esc(label), esc(value))
}

// encodeRepoRules serialises rules back into the kvlist storage format.
func encodeRepoRules(rules []RepoRule) (string, error) {
	rows := make([]map[string]string, 0, len(rules))
	for _, r := range rules {
		rows = append(rows, map[string]string{
			"repo":            r.Repo,
			"branch_pattern":  r.BranchPattern,
			"message_pattern": r.MessagePattern,
			"protected":       r.Protected,
			"force_push":      r.ForcePush,
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
	if p := strings.TrimSpace(r.MessagePattern); p != "" && p != "-" {
		if _, err := regexpCompile(p); err != nil {
			return "commit message pattern is not a valid regex: " + err.Error()
		}
	}
	switch strings.TrimSpace(r.ForcePush) {
	case "", "-", "true", "false":
	default:
		return `force_push must be true, false, empty (inherit) or "-" (inherit)`
	}
	return ""
}

// inheritLabel spells out what a rule column means. "" and "-" look nearly
// identical in a table cell but do opposite things, so the widget never shows a
// bare blank — it says which one it is.
func inheritLabel(v string) string {
	switch strings.TrimSpace(v) {
	case "":
		return "(inherit from global)"
	case "-":
		return "(cleared — global value does not apply)"
	default:
		return v
	}
}

// forcePushLabel is inheritLabel for the boolean column, where "-" is a synonym
// for inherit rather than a third state (see RepoRule).
func forcePushLabel(v string) string {
	switch strings.TrimSpace(v) {
	case "true":
		return "allowed"
	case "false":
		return "denied"
	case "-":
		return "(inherit from global)"
	default:
		return "(inherit from global)"
	}
}

// policyManagerInput drives the widget. Browser carries the field's own current
// value by the html= convention; the remaining fields are the names of the form
// controls this op's markup renders, which the widget posts back on any data-op
// click. A field missing here makes the matching c.Input read empty forever.
type policyManagerInput struct {
	Browser    string `wick:"desc=Current field value, supplied by the config UI."`
	SimRepo    string `wick:"desc=Repository to simulate against, as a path or host/owner/repo."`
	SimOp      string `wick:"desc=Operation to simulate. Example: push"`
	SimBranch  string `wick:"desc=Branch name to simulate."`
	RuleJSON   string `wick:"desc=Full replacement set of per-repo rules, as a JSON array."`
	SimMessage string `wick:"desc=Commit message to simulate. Optional."`

	// The fallback editor's controls. Named g_* so a reader can tell at a glance
	// which inputs belong to the global block rather than to a per-repo row.
	GBranch    string `wick:"desc=Fallback branch name pattern (regex)."`
	GMessage   string `wick:"desc=Fallback commit message pattern (regex)."`
	GProtected string `wick:"desc=Fallback protected branches, comma-separated globs."`
	GForce     string `wick:"desc=Present when force push is allowed; absent means denied."`

	// NewRepo is the sidebar's add box. The per-rule inputs are named r_*_<index>
	// and cannot be declared here — the index is only known at render time — so they
	// are read with c.Input directly. Declaring the prefix is not possible either;
	// the schema is a flat list of names.
	NewRepo string `wick:"desc=Repository glob for a new override."`
}

// doPolicyManager renders the editor plus an empty simulator.
func doPolicyManager(c *connector.Ctx) (any, error) {
	return map[string]any{"html": renderPolicyManager(c, nil)}, nil
}

// doPolicySimulate evaluates one hypothetical operation and re-renders with the
// verdict.
//
// It resolves through loadGlobal + ParseRepoRules + Resolve + Evaluate, which is
// what policyFor and execute do, so the answer here is the answer the operation
// gives. The repo string is passed as both the path and the slug candidate
// because MatchRepo tries both and the operator should not have to know which
// form a rule was written in.
func doPolicySimulate(c *connector.Ctx) (any, error) {
	repo := strings.TrimSpace(c.Input("sim_repo"))
	op := firstNonEmpty(strings.TrimSpace(c.Input("sim_op")), "push")
	branch := strings.TrimSpace(c.Input("sim_branch"))
	message := strings.TrimSpace(c.Input("sim_message"))

	global := loadGlobal(c)
	rules, err := ParseRepoRules(c.Cfg("repo_policies"))
	pol := Resolve(global, rules, repo, repo)
	if err != nil {
		// Mirror policyFor: malformed rules are recorded, not ignored, so the
		// simulator shows the same fail-closed behaviour the operations have.
		pol.PolicyErr = "repo_policies is not valid JSON: " + err.Error()
	}

	// A new branch triggers the name pattern; branch_create and checkout -b are
	// the operations that create one.
	newBranch := op == "branch_create"
	v := pol.Evaluate(Request{Op: op, Branch: branch, Message: message, NewBranch: newBranch})

	sim := &simResult{
		Repo: repo, Op: op, Branch: branch, Message: message, V: v, Pol: pol,
		Command: fmt.Sprintf("git %s origin %s", op, branch),
	}
	// Open the Simulator scope, not the fallback: the operator pressed Simulate and
	// the verdict lives in that panel, so a default render would hide the answer.
	return map[string]any{"html": renderPolicyManager(c, sim, selectedOpt("sim"))}, nil
}

// doPolicyRuleSave replaces the per-repo rule set from the editor.
//
// It returns the new value under {fields} so the core writes it to
// repo_policies, and the WHOLE widget under {html} rather than just a notice:
// HtmlField swaps the widget body for whatever html an op returns and does not
// re-fetch afterwards, so returning a bare one-liner would erase the editor and
// simulator from the page until the operator reloaded it.
//
// The re-render is driven by the rules just parsed, not by c.Cfg, because the
// {fields} write has not committed yet at this point — reading config here would
// show the operator their previous rule set and look like the save failed.
func doPolicyRuleSave(c *connector.Ctx) (any, error) {
	raw := strings.TrimSpace(c.Input("rule_json"))
	if raw == "" {
		raw = "[]"
	}
	rules, err := ParseRepoRules(raw)
	if err != nil {
		// Nothing is written: a malformed paste must not be able to destroy a
		// working rule set. Re-render from stored config so the rules still in
		// force stay on screen next to the error.
		return map[string]any{
			"html": renderPolicyManager(c, nil,
				noticeOpt("Could not save: "+err.Error(), false)),
		}, nil
	}
	encoded, err := encodeRepoRules(rules)
	if err != nil {
		return map[string]any{
			"html": renderPolicyManager(c, nil,
				noticeOpt("Could not save: "+err.Error(), false)),
		}, nil
	}

	msg := fmt.Sprintf("Saved %d rule(s).", len(rules))
	if warned := countWarnings(rules); warned > 0 {
		msg = fmt.Sprintf("Saved %d rule(s) — %d need attention, see below.", len(rules), warned)
	}
	return map[string]any{
		"fields": map[string]string{"repo_policies": encoded},
		"html": renderPolicyManager(c, nil,
			noticeOpt(msg, true), rulesOpt(rules)),
	}, nil
}

// doPolicyGlobalSave writes the fallback rules.
//
// Patterns are validated BEFORE anything is stored: a regex that does not compile
// blocks every mutating operation at runtime (fail-closed), so saving one silently
// would take the connector offline with no explanation on screen. Refusing the save
// and naming the error keeps the working policy in force.
func doPolicyGlobalSave(c *connector.Ctx) (any, error) {
	branch := strings.TrimSpace(c.Input("g_branch"))
	message := strings.TrimSpace(c.Input("g_message"))
	protected := splitCSV(c.Input("g_protected"))
	// An unchecked box sends nothing at all, so presence is the value.
	force := strings.TrimSpace(c.Input("g_force")) != ""

	for _, f := range []struct{ name, pat string }{
		{"Branch name pattern", branch},
		{"Commit message pattern", message},
	} {
		if f.pat == "" {
			continue
		}
		if _, err := regexpCompile(f.pat); err != nil {
			return map[string]any{
				"html": renderPolicyManager(c, nil,
					noticeOpt(f.name+" was not saved — "+err.Error(), false)),
			}, nil
		}
	}

	rows := make([]map[string]string, 0, len(protected))
	for _, brName := range protected {
		rows = append(rows, map[string]string{"branch": brName})
	}
	encoded, err := json.Marshal(rows)
	if err != nil {
		return map[string]any{
			"html": renderPolicyManager(c, nil, noticeOpt("Could not save: "+err.Error(), false)),
		}, nil
	}

	fields := map[string]string{
		"branch_name_pattern":    branch,
		"commit_message_pattern": message,
		"protected_branches":     string(encoded),
		"allow_force_push":       map[bool]string{true: "true", false: "false"}[force],
	}

	// The re-render reads the values just submitted rather than config, because the
	// {fields} write has not committed yet — reading config here would redraw the
	// previous state and look like the save was ignored.
	g := GlobalPolicy{
		BranchPattern:  branch,
		MessagePattern: message,
		Protected:      protected,
		AllowForcePush: force,
	}
	return map[string]any{
		"fields": fields,
		"html": renderPolicyManager(c, nil,
			noticeOpt("Fallback saved.", true), globalOpt(g)),
	}, nil
}

// doPolicyRuleAdd appends an override for the glob typed in the sidebar.
//
// Seeding every column empty is deliberate: an empty column inherits, so a brand
// new row changes nothing until the operator decides what it should override. A row
// pre-filled with the fallback's values would look like it inherits while actually
// pinning a copy that stops tracking later fallback edits.
func doPolicyRuleAdd(c *connector.Ctx) (any, error) {
	glob := strings.TrimSpace(c.Input("new_repo"))
	if glob == "" {
		return map[string]any{
			"html": renderPolicyManager(c, nil,
				noticeOpt("Enter a repository glob first, for example */org/infra.", false)),
		}, nil
	}

	rules, err := ParseRepoRules(c.Cfg("repo_policies"))
	if err != nil {
		return map[string]any{
			"html": renderPolicyManager(c, nil,
				noticeOpt("Stored rules are not valid JSON — repair them below before adding: "+err.Error(), false)),
		}, nil
	}
	for _, r := range rules {
		if r.Repo == glob {
			return map[string]any{
				"html": renderPolicyManager(c, nil,
					noticeOpt("There is already an override for "+glob+".", false)),
			}, nil
		}
	}

	rules = append(rules, RepoRule{Repo: glob})
	return saveRules(c, rules, fmt.Sprintf("Added %s. Every column inherits until you change it.", glob),
		fmt.Sprintf("r%d", len(rules)-1))
}

// doPolicyRuleUpdate writes one override from its panel.
func doPolicyRuleUpdate(c *connector.Ctx) (any, error) {
	i, rules, resp := ruleAt(c)
	if resp != nil {
		return resp, nil
	}

	idx := fmt.Sprintf("%d", i)
	next := RepoRule{
		Repo:           strings.TrimSpace(c.Input("r_repo_" + idx)),
		BranchPattern:  strings.TrimSpace(c.Input("r_branch_" + idx)),
		MessagePattern: strings.TrimSpace(c.Input("r_message_" + idx)),
		Protected:      strings.TrimSpace(c.Input("r_protected_" + idx)),
		ForcePush:      strings.TrimSpace(c.Input("r_force_" + idx)),
	}
	if next.Repo == "" {
		return map[string]any{
			"html": renderPolicyManager(c, nil,
				noticeOpt("A rule with no glob can never match. Delete it instead.", false),
				selectedOpt("r"+idx)),
		}, nil
	}
	// Validate before storing: a regex that does not compile blocks every mutation
	// at runtime, so saving one silently would take the connector offline. Both
	// pattern columns, for the same reason — Resolve treats either as fail-closed.
	for _, pat := range []struct{ label, value string }{
		{"branch pattern", next.BranchPattern},
		{"commit message pattern", next.MessagePattern},
	} {
		if pat.value == "" || pat.value == "-" {
			continue
		}
		if _, err := regexpCompile(pat.value); err != nil {
			return map[string]any{
				"html": renderPolicyManager(c, nil,
					noticeOpt("Not saved — "+pat.label+" does not compile: "+err.Error(), false),
					selectedOpt("r"+idx)),
			}, nil
		}
	}

	rules[i] = next
	return saveRules(c, rules, "Saved "+next.Repo+".", "r"+idx)
}

// doPolicyRuleClear marks every inheritable column on one override as cleared.
//
// This is why the operator never has to know that "-" is the marker: the button
// writes it. Typing it by hand is the error the widget exists to prevent, since a
// blank cell and a "-" cell look the same and do opposite things.
func doPolicyRuleClear(c *connector.Ctx) (any, error) {
	i, rules, resp := ruleAt(c)
	if resp != nil {
		return resp, nil
	}
	rules[i].BranchPattern = "-"
	rules[i].MessagePattern = "-"
	rules[i].Protected = "-"
	// ForcePush is a boolean with no cleared state, so "-" there means inherit —
	// setting it would be a lie. Left as it is.
	return saveRules(c, rules,
		"Cleared "+rules[i].Repo+" — the fallback's branch pattern, commit message pattern and protected list no longer apply there.",
		fmt.Sprintf("r%d", i))
}

// doPolicyRuleDelete removes one override.
func doPolicyRuleDelete(c *connector.Ctx) (any, error) {
	i, rules, resp := ruleAt(c)
	if resp != nil {
		return resp, nil
	}
	gone := rules[i].Repo
	rules = append(rules[:i:i], rules[i+1:]...)
	// Deleting shifts every later index, so returning to "r<i>" would open a
	// different rule than the one that was on screen. Fall back to the fallback.
	return saveRules(c, rules, "Deleted "+gone+".", "fallback")
}

// ruleAt resolves the data-arg index against the stored rules. It returns a ready
// response instead of an error when the index cannot be used, so callers stay flat.
func ruleAt(c *connector.Ctx) (int, []RepoRule, map[string]any) {
	rules, err := ParseRepoRules(c.Cfg("repo_policies"))
	if err != nil {
		return 0, nil, map[string]any{
			"html": renderPolicyManager(c, nil,
				noticeOpt("Stored rules are not valid JSON: "+err.Error(), false)),
		}
	}
	raw := strings.TrimSpace(c.Input("browser")) // data-arg arrives as "browser"
	i, convErr := strconv.Atoi(raw)
	if convErr != nil || i < 0 || i >= len(rules) {
		// Stale markup: the rules changed in another tab, or the widget was not
		// re-rendered after a delete.
		return 0, nil, map[string]any{
			"html": renderPolicyManager(c, nil,
				noticeOpt("That rule no longer exists — the list has been refreshed.", false)),
		}
	}
	return i, rules, nil
}

// saveRules persists a rule set and re-renders from the values just written.
//
// Reading config for the re-render would show the previous state, because the
// {fields} write has not committed when the op returns.
func saveRules(c *connector.Ctx, rules []RepoRule, msg, scope string) (any, error) {
	encoded, err := encodeRepoRules(rules)
	if err != nil {
		return map[string]any{
			"html": renderPolicyManager(c, nil, noticeOpt("Could not save: "+err.Error(), false)),
		}, nil
	}
	if warned := countWarnings(rules); warned > 0 {
		msg += fmt.Sprintf(" %d rule(s) need attention.", warned)
	}
	return map[string]any{
		"fields": map[string]string{"repo_policies": encoded},
		"html": renderPolicyManager(c, nil,
			noticeOpt(msg, true), rulesOpt(rules), selectedOpt(scope)),
	}, nil
}

func countWarnings(rules []RepoRule) int {
	var n int
	for _, r := range rules {
		if validateRule(r) != "" {
			n++
		}
	}
	return n
}

// renderNotice renders a one-line success or failure message.
func renderNotice(msg string, ok bool) string {
	color, mark := uiBad, "✕"
	if ok {
		color, mark = uiOK, "✓"
	}
	return fmt.Sprintf(`<div style="font-size:12px;color:%s;padding:6px 0">%s %s</div>`,
		color, mark, esc(msg))
}

// renderOpt tweaks one render: a banner to show, or a rule set to display in
// place of the stored one. Two knobs, so they are plain options rather than a
// struct threaded through every call site.
type renderOpt func(*renderState)

type renderState struct {
	notice   string
	rules    []RepoRule
	override bool
	samples  []string

	global    GlobalPolicy
	hasGlobal bool

	// selected is the scope tab to open on this render: "fallback" or "r<N>". After
	// a save the operator should still be looking at the row they just edited, not
	// be thrown back to the fallback.
	selected string
}

func noticeOpt(msg string, ok bool) renderOpt {
	return func(s *renderState) { s.notice = renderNotice(msg, ok) }
}

// rulesOpt renders the given rules instead of reading repo_policies, for the
// save path where the write has not committed yet.
func rulesOpt(rules []RepoRule) renderOpt {
	return func(s *renderState) { s.rules, s.override = rules, true }
}

// globalOpt renders the given fallback instead of reading config, for the save path
// where the {fields} write has not committed yet. Reading config there would redraw
// the previous values and look like the save was ignored.
func globalOpt(g GlobalPolicy) renderOpt {
	return func(s *renderState) { s.global, s.hasGlobal = g, true }
}

// selectedOpt opens a specific scope tab. Used after a save so the render returns
// the operator to the row they were editing.
func selectedOpt(scope string) renderOpt {
	return func(s *renderState) { s.selected = scope }
}

// samplesOpt supplies the repositories a glob is tested against in the "matches"
// line. Kept injectable so the widget can list real repos once a caller has them
// and so the behaviour is testable without any git checkout.
func samplesOpt(repos []string) renderOpt {
	return func(s *renderState) { s.samples = repos }
}

// renderPolicyManagerWithSamples renders the widget with an explicit repository
// list for the glob-match preview.
func renderPolicyManagerWithSamples(c *connector.Ctx, sim *simResult, repos []string) string {
	return renderPolicyManager(c, sim, samplesOpt(repos))
}

// renderPolicyManager renders the whole widget: the global fallback summary, one
// block per per-repo rule, the editor and the simulator.
//
// The global block is shown first and labelled as the fallback so the
// relationship between it and the overrides is visible in the layout rather than
// only in documentation.
// renderPolicyManager renders the whole policy panel: a list of scopes on the
// left and the selected scope on the right — a rule editor, or the simulator.
//
// The list-and-detail shape is what makes this readable. Stacked vertically, the
// fallback and every override competed for the same column and nothing on screen
// said which applied where; as a list, "Fallback" is simply the first entry and an
// override is another row you click. It also scales: ten repositories is ten rows,
// not ten expanded forms.
//
// The tabs are CSS-only — a hidden radio per scope plus `#id:checked ~` rules.
// Selectors target ids rather than positions: this widget's sibling markup has been
// reshaped twice already, and a positional selector silently pointed at the wrong
// element both times.
func renderPolicyManager(c *connector.Ctx, sim *simResult, opts ...renderOpt) string {
	st := &renderState{}
	for _, o := range opts {
		o(st)
	}

	global := loadGlobal(c)
	if st.hasGlobal {
		// A save supplied the values it just wrote; config still holds the old ones.
		global = st.global
	}

	rules, parseErr := st.rules, error(nil)
	if !st.override {
		rules, parseErr = ParseRepoRules(c.Cfg("repo_policies"))
	}

	// Repos each glob is tested against. The simulator's subject counts too, so a
	// glob being debugged right now shows whether it matches.
	samples := st.samples
	if sim != nil && strings.TrimSpace(sim.Repo) != "" {
		samples = append(append([]string(nil), samples...), sim.Repo)
	}

	const p = "pmw" // class prefix; every rule below is namespaced under it

	var b strings.Builder
	fmt.Fprint(&b, policyStyle(p, len(rules)))
	fmt.Fprintf(&b, `<div class="%s-w">`, p)

	if st.notice != "" {
		fmt.Fprint(&b, st.notice)
	}
	if parseErr != nil {
		fmt.Fprint(&b, renderNotice("Stored rules are not valid JSON: "+parseErr.Error()+
			" — use Raw JSON at the bottom to repair them.", false))
	}

	// Radios first: every panel and tab below is a following sibling, which is what
	// the ~ combinator needs.
	selected := strings.TrimSpace(st.selected)
	if selected == "" {
		selected = "fallback"
	}
	fmt.Fprintf(&b, `<input class="%[1]s-r" type="radio" name="%[1]s-scope" id="%[1]s-t-fallback"%[2]s/>`,
		p, checkedIf(selected == "fallback"))
	for i := range rules {
		id := fmt.Sprintf("%s-t-r%d", p, i)
		fmt.Fprintf(&b, `<input class="%s-r" type="radio" name="%s-scope" id="%s"%s/>`,
			p, p, id, checkedIf(selected == fmt.Sprintf("r%d", i)))
	}
	fmt.Fprintf(&b, `<input class="%[1]s-r" type="radio" name="%[1]s-scope" id="%[1]s-t-sim"%[2]s/>`,
		p, checkedIf(selected == "sim"))

	// ── the two columns ──
	fmt.Fprintf(&b, `<div class="%s-cols">`, p)

	// Left: the scope list.
	fmt.Fprintf(&b, `<div class="%s-list">`, p)
	fmt.Fprintf(&b, `<label class="%[1]s-item" for="%[1]s-t-fallback">`+
		`<span class="%[1]s-name">Fallback</span>`+
		`<span class="%[1]s-sub">every repository</span></label>`, p)

	for i, r := range rules {
		warn := validateRule(r)
		badge := scopeBadge(r.Repo)
		if warn != "" {
			badge = "needs attention"
		}
		cls := ""
		if warn != "" {
			cls = " " + p + "-bad"
		}
		fmt.Fprintf(&b, `<label class="%[1]s-item%[2]s" for="%[1]s-t-r%[3]d">`+
			`<span class="%[1]s-name">%[4]s</span>`+
			`<span class="%[1]s-sub">%[5]s</span></label>`,
			p, cls, i, esc(r.Repo), esc(badge))
	}
	fmt.Fprintf(&b, `<div class="%[1]s-add">`+
		`<input name="new_repo" placeholder="*/org/name or d:/code/*" class="%[1]s-in"/>`+
		`<button type="button" data-op="policy_rule_add" data-arg="" class="%[1]s-btn2">Add repository</button>`+
		`</div>`, p)
	// The simulator is a scope too: it is the one entry that asks a question about
	// the rules above rather than editing one, so it sits below the divider.
	fmt.Fprintf(&b, `<div class="%[1]s-sep"><label class="%[1]s-item" for="%[1]s-t-sim">`+
		`<span class="%[1]s-name">Simulator</span>`+
		`<span class="%[1]s-sub">try an operation</span></label></div>`, p)
	fmt.Fprint(&b, `</div>`) // list

	// Right: one panel per scope. All present, CSS reveals the selected one.
	fmt.Fprintf(&b, `<div class="%s-detail">`, p)
	fmt.Fprintf(&b, `<div class="%[1]s-panel" id="%[1]s-p-fallback">%[2]s</div>`,
		p, renderFallbackPanel(p, global))
	for i, r := range rules {
		fmt.Fprintf(&b, `<div class="%[1]s-panel" id="%[1]s-p-r%[2]d">%[3]s</div>`,
			p, i, renderRulePanel(p, i, r, samples))
	}
	fmt.Fprintf(&b, `<div class="%[1]s-panel" id="%[1]s-p-sim">%[2]s</div>`,
		p, renderSimulator(p, sim))
	fmt.Fprint(&b, `</div>`) // detail
	fmt.Fprint(&b, `</div>`) // cols

	fmt.Fprint(&b, rawJSONBlock(p, rules))
	fmt.Fprint(&b, `</div>`) // w
	return b.String()
}

func checkedIf(on bool) string {
	if on {
		return " checked"
	}
	return ""
}

// policyStyle is the widget's stylesheet. One block, every selector prefixed, and
// the reveal rules written per id — see renderPolicyManager for why not positional.
func policyStyle(p string, ruleCount int) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<style>
.%[1]s-w{color:%[2]s;font-size:13px}
.%[1]s-r{position:absolute;opacity:0;width:0;height:0;pointer-events:none}
.%[1]s-cols{display:grid;grid-template-columns:200px 1fr;gap:12px;align-items:start}
@media (max-width:820px){.%[1]s-cols{grid-template-columns:1fr}}
.%[1]s-list{display:flex;flex-direction:column;gap:2px;border:1px solid %[3]s;border-radius:8px;padding:6px;background:%[5]s}
.%[1]s-item{display:block;cursor:pointer;padding:6px 8px;border-radius:6px;border:1px solid transparent;user-select:none}
.%[1]s-item:hover{background:%[5]s}
.%[1]s-name{display:block;font-family:monospace;font-size:12px;word-break:break-all}
.%[1]s-sub{display:block;font-size:11px;opacity:.55;margin-top:1px}
.%[1]s-bad .%[1]s-sub{color:%[6]s;opacity:1}
.%[1]s-add{margin-top:6px;padding-top:6px;border-top:1px solid %[3]s;display:flex;flex-direction:column;gap:4px}
.%[1]s-detail{min-width:0}
.%[1]s-panel{display:none;border:1px solid %[3]s;border-radius:8px;padding:12px;background:%[5]s}
.%[1]s-h{font-weight:600;font-size:13px;margin-bottom:2px}
.%[1]s-hint{font-size:11px;opacity:.6;margin-bottom:10px}
.%[1]s-f{margin-bottom:14px}
.%[1]s-l{display:block;font-size:12px;font-weight:600;opacity:.9;margin-bottom:4px}
.%[1]s-in{width:100%%;box-sizing:border-box;font-family:monospace;font-size:12px;padding:7px 9px;border:1px solid %[3]s;border-radius:6px;background:%[4]s;color:%[2]s}
.%[1]s-in:focus{outline:none;border-color:%[7]s;box-shadow:0 0 0 3px %[7]s26}
.%[1]s-in::placeholder{opacity:.45}
.%[1]s-help{font-size:11px;opacity:.6;margin-top:3px}
.%[1]s-err{font-size:11px;color:%[6]s;margin-top:3px}
.%[1]s-cb{display:flex;gap:7px;align-items:flex-start;font-size:12px;cursor:pointer;margin-bottom:10px}
.%[1]s-actions{display:flex;gap:6px;flex-wrap:wrap;border-top:1px solid %[3]s;padding-top:10px}
.%[1]s-btn{font-size:12px;padding:7px 13px;border-radius:6px;border:1px solid %[7]s;background:%[7]s;color:#fff;font-weight:600;cursor:pointer}
.%[1]s-btn2{font-size:12px;padding:7px 12px;border-radius:6px;border:1px solid %[3]s;background:%[4]s;color:%[2]s;cursor:pointer;font-weight:500}
.%[1]s-btn2:hover{border-color:%[7]s}
.%[1]s-btn3{font-size:12px;padding:7px 12px;border-radius:6px;border:1px solid %[6]s;background:%[4]s;color:%[6]s;cursor:pointer;font-weight:500}
.%[1]s-match{font-size:11px;opacity:.7;margin-top:3px;font-family:monospace}
.%[1]s-raw{margin-top:12px}
.%[1]s-raw summary{cursor:pointer;font-size:11px;opacity:.6}
.%[1]s-ta{width:100%%;box-sizing:border-box;font-family:monospace;font-size:11px;padding:6px;margin-top:6px;border:1px solid %[3]s;border-radius:6px;background:%[5]s;color:%[2]s}
.%[1]s-sim{display:grid;grid-template-columns:1fr 1fr;gap:14px;align-items:start}
@media (max-width:820px){.%[1]s-sim{grid-template-columns:1fr}}
.%[1]s-empty{border:1px dashed %[3]s;border-radius:8px;padding:14px;font-size:12px;opacity:.6;line-height:1.5}
.%[1]s-sep{margin-top:6px;padding-top:6px;border-top:1px solid %[3]s}
`, p, uiText, uiBorder, uiPanel, uiSunken, uiBad, uiOK)

	// Selected tab, and its panel. Ids, not positions.
	fmt.Fprintf(&b, `
#%[1]s-t-fallback:checked~.%[1]s-cols label[for="%[1]s-t-fallback"]{background:%[2]s1a;border-color:%[2]s;font-weight:600}
#%[1]s-t-fallback:checked~.%[1]s-cols #%[1]s-p-fallback{display:block}
#%[1]s-t-sim:checked~.%[1]s-cols label[for="%[1]s-t-sim"]{background:%[2]s1a;border-color:%[2]s;font-weight:600}
#%[1]s-t-sim:checked~.%[1]s-cols #%[1]s-p-sim{display:block}
`, p, uiOK)
	for i := 0; i < ruleCount; i++ {
		fmt.Fprintf(&b, `
#%[1]s-t-r%[2]d:checked~.%[1]s-cols label[for="%[1]s-t-r%[2]d"]{background:%[3]s1a;border-color:%[3]s;font-weight:600}
#%[1]s-t-r%[2]d:checked~.%[1]s-cols #%[1]s-p-r%[2]d{display:block}
`, p, i, uiOK)
	}
	fmt.Fprintf(&b, `</style>`)
	return b.String()
}

// renderFallbackPanel is the editable fallback: what applies when no override
// matches. Each field shows the raw pattern (what is stored) and, below it, what
// that value means — "empty" and "any name is accepted" are the same state, and
// only the second reads as a decision.
func renderFallbackPanel(p string, g GlobalPolicy) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<div class="%s-h">Fallback</div>`, p)
	fmt.Fprintf(&b, `<div class="%s-hint">Applies to every repository. Any override on the left wins over these.</div>`, p)

	// Same specs the override panel uses, so the two read identically apart from the
	// inherit sentence only an override needs.
	specField(&b, p, "g_branch", branchSpec, g.BranchPattern, fallbackHelp(branchSpec, g.BranchPattern))
	specField(&b, p, "g_message", messageSpec, g.MessagePattern, fallbackHelp(messageSpec, g.MessagePattern))
	protected := strings.Join(g.Protected, ", ")
	specField(&b, p, "g_protected", protectedSpec, protected, fallbackHelp(protectedSpec, protected))

	// A checkbox, not a text field: the browser sends nothing when it is unchecked,
	// so the save reads presence rather than parsing a word.
	fmt.Fprintf(&b, `<label class="%[1]s-cb"><input type="checkbox" name="g_force" value="true"%[2]s/>`+
		`<span>Allow force push<span style="opacity:.6"> — --force-with-lease on push, and reset --hard</span></span></label>`,
		p, checkedIf(g.AllowForcePush))

	fmt.Fprintf(&b, `<div class="%[1]s-actions">`+
		`<button type="button" data-op="policy_global_save" data-arg="" class="%[1]s-btn">Save fallback</button>`+
		`</div>`, p)
	return b.String()
}

// renderRulePanel is one override. Its inputs are named with the rule's index so a
// save knows which row it edited.
func renderRulePanel(p string, i int, r RepoRule, samples []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<div class="%s-h">%s</div>`, p, esc(r.Repo))

	fmt.Fprintf(&b, `<div class="%s-hint">%s</div>`, p, esc(scopeHint(r.Repo)))

	if warn := validateRule(r); warn != "" {
		fmt.Fprintf(&b, `<div class="%s-err">%s</div>`, p, esc(warn))
	}

	idx := fmt.Sprintf("%d", i)
	// The match line belongs under the glob it describes, not above the panel: it
	// answers "does this actually hit anything?" about that one field.
	field(&b, p, "r_repo_"+idx, "Repository glob", globPlaceholder, r.Repo,
		globHelp(r.Repo, samples), "")
	specField(&b, p, "r_branch_"+idx, branchSpec, r.BranchPattern, overrideHelp(branchSpec, r.BranchPattern))
	// A per-repo commit rule is the point of the override: one team's repo wants
	// Conventional Commits, another wants a ticket id, and the fallback cannot say both.
	specField(&b, p, "r_message_"+idx, messageSpec, r.MessagePattern, overrideHelp(messageSpec, r.MessagePattern))
	specField(&b, p, "r_protected_"+idx, protectedSpec, r.Protected, overrideHelp(protectedSpec, r.Protected))

	// force_push is a tri-state stored as text: inherit / true / false. A select
	// says that plainly; a checkbox cannot express "inherit" at all.
	fmt.Fprintf(&b, `<div class="%[1]s-f"><label class="%[1]s-l" for="%[1]s-r_force_%[2]s">Force push</label>`+
		`<select id="%[1]s-r_force_%[2]s" name="r_force_%[2]s" class="%[1]s-in">`, p, idx)
	for _, o := range []struct{ val, label string }{
		{"", "Inherit from fallback"},
		{"true", "Allowed"},
		{"false", "Denied"},
	} {
		sel := ""
		if normForce(r.ForcePush) == o.val {
			sel = " selected"
		}
		fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`, esc(o.val), sel, esc(o.label))
	}
	fmt.Fprintf(&b, `</select></div>`)

	fmt.Fprintf(&b, `<div class="%[1]s-actions">`+
		`<button type="button" data-op="policy_rule_update" data-arg="%[2]s" class="%[1]s-btn">Save</button>`+
		`<button type="button" data-op="policy_rule_clear" data-arg="%[2]s" class="%[1]s-btn2">Clear inherited</button>`+
		`<button type="button" data-op="policy_rule_delete" data-arg="%[2]s" class="%[1]s-btn3">Delete</button>`+
		`</div>`, p, idx)
	return b.String()
}

// specField renders a field from its spec: label and placeholder come from the spec
// so the two panels cannot drift apart, and the regex error is derived rather than
// passed in — a pattern field that forgot to report a bad regex would leave the
// connector refusing every mutation with no visible cause.
func specField(b *strings.Builder, p, name string, sp fieldSpec, value, help string) {
	errMsg := ""
	if sp.syntax == reSyntax {
		errMsg = patternErr(value)
	}
	field(b, p, name, sp.label, sp.placeholder, value, help, errMsg)
}

// field renders one labelled input with its help line and, when the value is a
// regex that does not compile, the compiler's own message.
func field(b *strings.Builder, p, name, label, placeholder, value, help, errMsg string) {
	fmt.Fprintf(b, `<div class="%[1]s-f">`, p)
	fmt.Fprintf(b, `<label class="%[1]s-l" for="%[1]s-%[2]s">%[3]s</label>`, p, esc(name), esc(label))
	fmt.Fprintf(b, `<input id="%[1]s-%[2]s" name="%[2]s" value="%[3]s" placeholder="%[4]s" class="%[1]s-in"/>`,
		p, esc(name), esc(value), esc(placeholder))
	if errMsg != "" {
		fmt.Fprintf(b, `<div class="%s-err">%s</div>`, p, esc(errMsg))
	} else if help != "" {
		fmt.Fprintf(b, `<div class="%s-help">%s</div>`, p, esc(help))
	}
	fmt.Fprint(b, `</div>`)
}

// Placeholders. Every one is a WORKING value for its field, not a description of
// the shape: the operator's first question is "what do I type here", and a real
// example answers it in a way "enter a regex" never does. Shared between the
// fallback and the overrides so the two panels teach the same syntax.
const (
	// Globs, not regexes — the repo and protected columns are matched with
	// path.Match, where * is the only metacharacter.
	globPlaceholder      = "*/org/infra   or   d:/code/work/*"
	protectedPlaceholder = "main, master, release/*"

	// Regexes, RE2. Unanchored by default, which is the trap: "fix/" matches
	// "hotfix/x" too, so both samples are anchored.
	branchPlaceholder  = `^(fix|feat|chore)/[a-z0-9._-]+$`
	messagePlaceholder = `^(feat|fix|chore)(\(.+\))?: .{10,}`
)

// The help lines below turn a stored value into its consequence, and name the
// syntax the field expects. Two things an operator cannot get from the field
// itself: an empty pattern cannot say "then anything is accepted", and a text box
// cannot say whether it wants a regex or a glob — mixing the two up silently
// produces a rule that matches nothing.

// fieldSpec is everything a policy field says about itself: what it gates, what it
// accepts, and what an empty value means.
//
// One spec per field, shared by BOTH panels, because the fallback and an override
// edit the SAME rule — the only real difference is which layer the value lands on.
// Building the two help lines from separate code produced two different vocabularies
// for one field ("RE2 regex, unanchored" in the fallback, "replaces the fallback's
// branch pattern" in the override), so the panel that actually needed the syntax
// never stated it.
type fieldSpec struct {
	label       string // the form label, identical in both panels
	placeholder string // a WORKING example, not a description of the shape
	gates       string // which operations this value is checked on
	syntax      string // the language it is written in
	emptyMeans  string // the consequence of leaving it blank, in the fallback
	noun        string // how the override refers to it ("branch pattern")
}

// The specs. Wording lives here and nowhere else.
var (
	branchSpec = fieldSpec{
		label:       "Branch name pattern",
		placeholder: branchPlaceholder,
		gates:       "Checked when a branch is CREATED; pushing to a branch that already exists is not affected.",
		syntax:      reSyntax,
		emptyMeans:  "any name is accepted when a branch is created",
		noun:        "branch pattern",
	}
	messageSpec = fieldSpec{
		label:       "Commit message pattern",
		placeholder: messagePlaceholder,
		gates:       "Checked on commit only, against the whole message — a push carries no message of its own.",
		syntax:      reSyntax,
		emptyMeans:  "any commit message is accepted",
		noun:        "commit message pattern",
	}
	protectedSpec = fieldSpec{
		label:       "Protected branches",
		placeholder: protectedPlaceholder,
		gates:       "Direct push, commit, merge and pull are refused on these. Reads are unaffected.",
		syntax:      globSyntax,
		emptyMeans:  "no branch is protected, so a direct push to main is allowed",
		noun:        "protected list",
	}
)

// fallbackHelp is the help line in the Fallback panel: what the field gates, in
// which language, and — when blank — what that blankness permits.
func fallbackHelp(sp fieldSpec, value string) string {
	if strings.TrimSpace(value) == "" {
		return "Empty — " + sp.emptyMeans + ". " + sp.syntax
	}
	return sp.gates + " " + sp.syntax
}

// overrideHelp is the same line for a per-repo panel, with one sentence prepended
// for the thing only an override has: which layer wins.
//
// The syntax and the gating are stated here too, at every state including empty.
// They are properties of the field, not of the fallback, and the override panel is
// where a NEW pattern actually gets typed.
func overrideHelp(sp fieldSpec, value string) string {
	switch strings.TrimSpace(value) {
	case "":
		return "Empty — inherits the fallback's " + sp.noun + ". Type a value to override it for matching repositories. " + sp.syntax
	case "-":
		return "Cleared — the fallback's " + sp.noun + " does not apply here, so " + sp.emptyMeans + "."
	default:
		return "Overrides the fallback for matching repositories. " + sp.gates + " " + sp.syntax
	}
}

// reSyntax and globSyntax name the language a field is written in. Stated at every
// state, empty included: a text box cannot say whether it wants a regex or a glob,
// and a glob typed into a pattern column compiles as a regex and then matches almost
// nothing. Anchoring is called out because an unanchored pattern is the mistake that
// silently lets everything through — "fix/" alone also accepts "hotfix/nope".
const (
	reSyntax   = `Accepts an RE2 regex (Go's regexp) — anchor it with ^…$ or it matches anywhere in the value.`
	globSyntax = "Accepts comma-separated globs where * is the only wildcard — not regexes."
)

// scopeBadge is the one-line description under a scope in the list. Wildcard count
// is what the engine sorts on, but "2 wildcards" tells a reader nothing — what they
// need is how broad the rule is and who wins.
func scopeBadge(glob string) string {
	switch n := Specificity(glob); n {
	case 0:
		return "exact match"
	case 1:
		return "matches a group"
	default:
		return "matches broadly"
	}
}

// scopeHint expands the same idea in the panel, where there is room to say why it
// matters.
func scopeHint(glob string) string {
	switch n := Specificity(glob); n {
	case 0:
		return "Applies to this one repository. An exact match beats any pattern."
	case 1:
		return "Applies to every repository matching this pattern. Beats broader patterns; an exact match beats it."
	default:
		return "Applies broadly. Any more specific rule wins over this one."
	}
}

// globHelp is the glob field's help line: what it matches against, and — once some
// repositories are known — whether it actually hits any of them.
func globHelp(glob string, samples []string) string {
	base := "Matched against both the local path and host/owner/repo."
	hits := matchSummary(glob, samples)
	if hits == "" || strings.HasPrefix(hits, "(none of") {
		// Saying "matches nothing" would be misleading when nothing has been tested
		// yet — the widget only knows repositories the simulator has seen.
		return base
	}
	return base + " Currently matches: " + hits
}

// patternErr returns the compiler's message for a regex that does not compile, or
// "" when the value is empty or valid. Shown in place of the help line, because a
// broken pattern blocks every mutation at runtime and that is the more urgent fact.
func patternErr(pat string) string {
	pat = strings.TrimSpace(pat)
	if pat == "" || pat == "-" {
		return ""
	}
	if _, err := regexpCompile(pat); err != nil {
		return "Does not compile: " + err.Error() + " — mutations stay blocked until this is fixed."
	}
	return ""
}

// normForce maps the stored tri-state onto the three select values. "-" and "" are
// the same thing for a boolean (see RepoRule).
func normForce(v string) string {
	switch strings.TrimSpace(v) {
	case "true":
		return "true"
	case "false":
		return "false"
	default:
		return ""
	}
}

// rawJSONBlock is the escape hatch. The config fields are hidden, so if a button
// here ever fails there would otherwise be no way to repair a rule set — this stays
// collapsed and out of the way until it is needed.
func rawJSONBlock(p string, rules []RepoRule) string {
	encoded, _ := encodeRepoRules(rules)
	var b strings.Builder
	fmt.Fprintf(&b, `<details class="%s-raw">`, p)
	fmt.Fprintf(&b, `<summary>Raw JSON — for repairing rules by hand</summary>`)
	fmt.Fprintf(&b, `<textarea name="rule_json" rows="4" class="%s-ta">%s</textarea>`, p, esc(encoded))
	fmt.Fprintf(&b, `<div class="%[1]s-actions" style="margin-top:6px">`+
		`<button type="button" data-op="policy_rule_save" data-arg="" class="%[1]s-btn2">Replace all rules</button>`+
		`</div>`, p)
	fmt.Fprint(&b, `</details>`)
	return b.String()
}

// matchSummary names the sample repositories a glob matches, using the same
// MatchRepo the resolver uses so the preview cannot disagree with the outcome.
func matchSummary(glob string, samples []string) string {
	seen := make(map[string]bool, len(samples))
	hits := make([]string, 0, len(samples))
	for _, s := range samples {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		if MatchRepo(glob, s, s) {
			hits = append(hits, s)
		}
	}
	if len(hits) == 0 {
		return "(none of the repositories checked)"
	}
	sort.Strings(hits)
	return strings.Join(hits, ", ")
}

// renderSimulator renders the "what would happen if" form and, when a run has
// happened, its verdict.
func renderSimulator(p string, sim *simResult) string {
	simRepo, simOp, simBranch, simMessage := "", "push", "", ""
	if sim != nil {
		simRepo, simOp, simBranch, simMessage = sim.Repo, sim.Op, sim.Branch, sim.Message
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<div class="%s-h">Simulator</div>`, p)
	fmt.Fprintf(&b, `<div class="%s-hint">Answers what would happen, using the same policy code the operations use. Nothing is executed.</div>`, p)

	// Two columns: the question on the left, the answer on the right once there is
	// one. Stacked, the verdict landed far below the inputs that produced it.
	fmt.Fprintf(&b, `<div class="%s-sim">`, p)

	fmt.Fprint(&b, `<div>`)
	field(&b, p, "sim_repo", "Repository", "abc.com/org/infra or d:/code/work/api", simRepo,
		"Which rule applies is decided from this.", "")

	fmt.Fprintf(&b, `<div class="%[1]s-f"><label class="%[1]s-l" for="%[1]s-sim_op">Operation</label>`+
		`<select id="%[1]s-sim_op" name="sim_op" class="%[1]s-in">`, p)
	for _, op := range []string{"push", "commit", "branch_create", "checkout", "merge", "reset", "rebase", "tag", "pull"} {
		sel := ""
		if op == simOp {
			sel = " selected"
		}
		fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`, esc(op), sel, esc(op))
	}
	fmt.Fprintf(&b, `</select><div class="%s-help">%s</div></div>`, p, esc(opHelp(simOp)))

	field(&b, p, "sim_branch", "Branch", "fix/login-bug", simBranch,
		"The branch the operation targets.", "")
	// Optional: only a commit is judged on a message, so an empty value means "do
	// not ask about the message" rather than "an empty message".
	field(&b, p, "sim_message", "Commit message", "fix: something", simMessage,
		"Only checked when the operation is commit.", "")

	fmt.Fprintf(&b, `<button type="button" data-op="policy_simulate" data-arg="" class="%s-btn">Simulate</button>`, p)
	fmt.Fprint(&b, `</div>`)

	fmt.Fprint(&b, `<div>`)
	if sim != nil {
		fmt.Fprint(&b, renderSimulation(*sim))
	} else {
		fmt.Fprintf(&b, `<div class="%s-empty">Fill in the left and press Simulate. The answer names the rule that decided and the command that would run.</div>`, p)
	}
	fmt.Fprint(&b, `</div>`)

	fmt.Fprint(&b, `</div>`)
	return b.String()
}

// opHelp explains which rules the chosen operation is actually subject to. This is
// the thing most easily got wrong about the policy: a branch pattern does not gate a
// push, and a commit message rule does not gate anything except a commit.
func opHelp(op string) string {
	switch op {
	case "commit":
		return "Judged on the protected list and the commit message pattern."
	case "branch_create", "checkout":
		return "Judged on the branch name pattern and the protected list."
	case "push":
		return "Judged on the protected list. The branch pattern does not apply to an existing branch."
	case "reset":
		return "A hard reset also needs force push to be allowed."
	default:
		return "Judged on the protected list."
	}
}
