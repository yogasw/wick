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
	Browser   string `wick:"desc=Current field value, supplied by the config UI."`
	SimRepo   string `wick:"desc=Repository to simulate against, as a path or host/owner/repo."`
	SimOp     string `wick:"desc=Operation to simulate. Example: push"`
	SimBranch string `wick:"desc=Branch name to simulate."`
	RuleJSON  string `wick:"desc=Full replacement set of per-repo rules, as a JSON array."`
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
	v := pol.Evaluate(Request{Op: op, Branch: branch, NewBranch: newBranch})

	sim := &simResult{
		Repo: repo, Op: op, Branch: branch, V: v, Pol: pol,
		Command: fmt.Sprintf("git %s origin %s", op, branch),
	}
	return map[string]any{"html": renderPolicyManager(c, sim)}, nil
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
}

func noticeOpt(msg string, ok bool) renderOpt {
	return func(s *renderState) { s.notice = renderNotice(msg, ok) }
}

// rulesOpt renders the given rules instead of reading repo_policies, for the
// save path where the write has not committed yet.
func rulesOpt(rules []RepoRule) renderOpt {
	return func(s *renderState) { s.rules, s.override = rules, true }
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
func renderPolicyManager(c *connector.Ctx, sim *simResult, opts ...renderOpt) string {
	st := &renderState{}
	for _, o := range opts {
		o(st)
	}

	global := loadGlobal(c)
	rules, parseErr := st.rules, error(nil)
	if !st.override {
		rules, parseErr = ParseRepoRules(c.Cfg("repo_policies"))
	}

	// Repos to test each glob against. The simulator's subject counts too, so a
	// glob the operator is actively debugging shows whether it matches.
	samples := st.samples
	if sim != nil && strings.TrimSpace(sim.Repo) != "" {
		samples = append(append([]string(nil), samples...), sim.Repo)
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<div style="font-family:inherit;color:%s">`, uiText)

	if st.notice != "" {
		fmt.Fprint(&b, st.notice)
	}

	fmt.Fprint(&b, renderGlobalBlock(global))

	if parseErr != nil {
		fmt.Fprint(&b, renderNotice("repo_policies is not valid JSON: "+parseErr.Error(), false))
	}

	fmt.Fprintf(&b, `<div style="font-size:12px;font-weight:700;margin:10px 0 6px">PER-REPO RULES (%d) — an override wins over GLOBAL</div>`,
		len(rules))
	if len(rules) == 0 {
		fmt.Fprintf(&b, `<div style="font-size:12px;opacity:.7;margin-bottom:8px">No overrides. Every repository uses the GLOBAL rules above.</div>`)
	}
	for i, r := range rules {
		fmt.Fprint(&b, renderRuleBlock(i+1, r, samples))
	}

	fmt.Fprint(&b, renderEditor(rules))
	fmt.Fprint(&b, renderSimulator(sim))

	fmt.Fprint(&b, `</div>`)
	return b.String()
}

// renderGlobalBlock renders the fallback layer, first and labelled as such.
func renderGlobalBlock(g GlobalPolicy) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<div style="border:1px solid %s;border-radius:8px;padding:10px;margin-bottom:10px;background:%s">`,
		uiBorder, uiSunken)
	fmt.Fprintf(&b, `<div style="font-size:12px;font-weight:700;margin-bottom:6px">GLOBAL — the fallback, used when no rule below matches</div>`)

	pat := g.BranchPattern
	if pat == "" {
		pat = "(none — any branch name is accepted)"
	}
	fmt.Fprint(&b, kvRow("branch pattern", pat))

	prot := strings.Join(g.Protected, ", ")
	if prot == "" {
		prot = "(none)"
	}
	fmt.Fprint(&b, kvRow("protected", prot))

	force := "denied"
	if g.AllowForcePush {
		force = "allowed"
	}
	fmt.Fprint(&b, kvRow("force push", force))

	fmt.Fprintf(&b, `<div style="font-size:11px;opacity:.6;margin-top:6px">Edit these in the Branch Policy section above.</div>`)
	fmt.Fprint(&b, `</div>`)
	return b.String()
}

// renderRuleBlock renders one override: its wildcard count, which sample repos
// it matches, each column in words, and any validation warning.
func renderRuleBlock(n int, r RepoRule, samples []string) string {
	warn := validateRule(r)
	border := uiBorder
	if warn != "" {
		border = uiBad
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<div style="border:1px solid %s;border-radius:8px;padding:10px;margin-bottom:8px;background:%s">`,
		border, uiPanel)
	fmt.Fprintf(&b, `<div style="font-size:12px;font-weight:600;margin-bottom:4px">RULE %d — <span style="font-family:monospace">%s</span> `+
		`<span style="opacity:.6;font-weight:400">(%d wildcard(s); fewer wins)</span></div>`,
		n, esc(r.Repo), Specificity(r.Repo))

	if len(samples) > 0 {
		fmt.Fprint(&b, kvRow("matches", matchSummary(r.Repo, samples)))
	}
	fmt.Fprint(&b, kvRow("branch pattern", inheritLabel(r.BranchPattern)))
	fmt.Fprint(&b, kvRow("protected", inheritLabel(r.Protected)))
	fmt.Fprint(&b, kvRow("force push", forcePushLabel(r.ForcePush)))

	if warn != "" {
		fmt.Fprint(&b, renderNotice(warn, false))
	}
	fmt.Fprint(&b, `</div>`)
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

// renderEditor renders the rule editor.
//
// The textarea is the transport — one round-trip writes the whole set, which
// sidesteps the sequential-write problem a per-rule field would create. The
// buttons above it are the reason the operator never types "-": Add rule seeds a
// well-formed row with its columns spelled out, and the two Clear buttons append
// a row that already carries the marker.
func renderEditor(rules []RepoRule) string {
	current, err := encodeRepoRules(rules)
	if err != nil {
		current = "[]"
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<div style="border:1px solid %s;border-radius:8px;padding:10px;margin-bottom:10px;background:%s">`,
		uiBorder, uiPanel)
	fmt.Fprintf(&b, `<div style="font-size:12px;font-weight:600;margin-bottom:6px">Edit rules</div>`)
	fmt.Fprintf(&b, `<textarea name="rule_json" rows="7" spellcheck="false" style="width:100%%;box-sizing:border-box;font-family:monospace;font-size:12px;padding:6px;border:1px solid %s;border-radius:6px;background:%s;color:%s">%s</textarea>`,
		uiBorder, uiPanel, uiText, esc(current))

	fmt.Fprintf(&b, `<div style="font-size:11px;opacity:.75;margin:6px 0">`+
		`<div><span style="font-family:monospace">repo</span> — glob matched against the checkout path or host/owner/repo. Example: <span style="font-family:monospace">*/org/*</span></div>`+
		`<div><span style="font-family:monospace">branch_pattern</span> — regex a new branch must match. Checked when you save.</div>`+
		`<div><span style="font-family:monospace">protected</span> — comma-separated globs.</div>`+
		`<div><span style="font-family:monospace">force_push</span> — <span style="font-family:monospace">true</span> or <span style="font-family:monospace">false</span>.</div>`+
		`<div style="margin-top:4px">Leave a column <b>empty to inherit</b> the GLOBAL value, or set it to <span style="font-family:monospace">"-"</span> to <b>clear</b> it so no GLOBAL value applies. Use the buttons below rather than typing the marker.</div>`+
		`</div>`)

	// Templates. data-op="__select" is reserved by the widget for storing a
	// value, so these carry no data-op: they are inert examples the operator
	// copies, which keeps the whole editor inside one save round-trip.
	fmt.Fprintf(&b, `<details style="margin-bottom:6px"><summary style="font-size:11px;cursor:pointer;opacity:.8">Row templates — copy one into the box above</summary>`)
	tpl := func(label, row string) {
		fmt.Fprintf(&b, `<div style="font-size:11px;margin-top:4px"><div style="opacity:.7">%s</div>`+
			`<div style="font-family:monospace;padding:4px 6px;border:1px dashed %s;border-radius:4px;background:%s;word-break:break-all">%s</div></div>`,
			esc(label), uiBorder, uiSunken, esc(row))
	}
	tpl("Inherit everything except the branch pattern",
		`{"repo":"*/org/api","branch_pattern":"^(fix|feat)/.+$","protected":"","force_push":""}`)
	tpl("Clear the protected list for a sandbox — no branch is protected there",
		`{"repo":"*/org/sandbox","branch_pattern":"-","protected":"-","force_push":"true"}`)
	tpl("Tighten one repository — protect more, deny force push",
		`{"repo":"*/org/infra","branch_pattern":"^ops/.+$","protected":"master,main,release/*","force_push":"false"}`)
	fmt.Fprint(&b, `</details>`)

	fmt.Fprintf(&b, `<button type="button" data-op="policy_rule_save" data-arg="" style="font-size:12px;padding:5px 10px;border-radius:6px;border:1px solid %s;background:%s;color:#fff;cursor:pointer">Save rules</button>`,
		uiOK, uiOK)
	fmt.Fprintf(&b, `<span style="font-size:11px;opacity:.6;margin-left:8px">Every regex is compiled on save; problems are reported above.</span>`)
	fmt.Fprint(&b, `</div>`)
	return b.String()
}

// renderSimulator renders the "what would happen if" form and, when a run has
// happened, its verdict.
func renderSimulator(sim *simResult) string {
	simRepo, simOp, simBranch := "", "push", ""
	if sim != nil {
		simRepo, simOp, simBranch = sim.Repo, sim.Op, sim.Branch
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<div style="border:1px solid %s;border-radius:8px;padding:10px;background:%s">`,
		uiBorder, uiPanel)
	fmt.Fprintf(&b, `<div style="font-size:12px;font-weight:600;margin-bottom:2px">Policy simulator</div>`)
	fmt.Fprintf(&b, `<div style="font-size:11px;opacity:.65;margin-bottom:6px">Runs the same policy code the operations run. Nothing is executed.</div>`)

	field := func(name, label, placeholder, value string) {
		fmt.Fprintf(&b, `<label style="display:block;font-size:11px;opacity:.7;margin-bottom:2px">%s</label>`, esc(label))
		fmt.Fprintf(&b, `<input name="%s" placeholder="%s" value="%s" style="width:100%%;box-sizing:border-box;font-size:12px;padding:5px;margin-bottom:6px;border:1px solid %s;border-radius:6px;background:%s;color:%s"/>`,
			esc(name), esc(placeholder), esc(value), uiBorder, uiPanel, uiText)
	}
	field("sim_repo", "Repository (path or host/owner/repo)", "abc.com/org/infra or d:/code/work/api", simRepo)

	fmt.Fprintf(&b, `<label style="display:block;font-size:11px;opacity:.7;margin-bottom:2px">Operation</label>`)
	fmt.Fprintf(&b, `<select name="sim_op" style="width:100%%;box-sizing:border-box;font-size:12px;padding:5px;margin-bottom:6px;border:1px solid %s;border-radius:6px;background:%s;color:%s">`,
		uiBorder, uiPanel, uiText)
	for _, op := range []string{"push", "commit", "branch_create", "checkout", "merge", "reset", "rebase", "tag", "pull"} {
		sel := ""
		if op == simOp {
			sel = ` selected`
		}
		fmt.Fprintf(&b, `<option value="%s"%s>%s</option>`, esc(op), sel, esc(op))
	}
	fmt.Fprint(&b, `</select>`)

	field("sim_branch", "Branch", "fix/login-bug", simBranch)

	fmt.Fprintf(&b, `<button type="button" data-op="policy_simulate" data-arg="" style="font-size:12px;padding:5px 10px;border-radius:6px;border:1px solid %s;background:transparent;color:%s;cursor:pointer">Simulate</button>`,
		uiBorder, uiText)

	if sim != nil {
		fmt.Fprint(&b, renderSimulation(*sim))
	}
	fmt.Fprint(&b, `</div>`)
	return b.String()
}
