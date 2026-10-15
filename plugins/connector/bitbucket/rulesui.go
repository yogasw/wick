// rulesui.go implements the "Merge rules" test panel on the config page: paste
// a pull request, see the checklist merge_pull_request would apply to it. It
// runs the same checkMergeRules the merge op runs, against the SAVED rules, and
// never merges.
package main

import (
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// Theme tokens. wick's theme variables hold bare RGB triplets ("255 255 255"),
// so they only work wrapped in rgb(), and dark mode is a "dark" class on
// <html>. themeCSS maps them to a few local variables, light and dark, using
// the same pairs as the manager's own fields (white-100/navy-800 field,
// white-400/navy-600 border, black-900/white-100 text), so the widgets follow
// every theme. Green and neg are fixed across themes, hence the literal values
// of green-500 and neg-400.
const (
	uiText   = "var(--bb-fg)"
	uiBorder = "var(--bb-border)"
	uiPanel  = "var(--bb-surface)"
	uiSunken = "var(--bb-field)"
	uiOK     = "#27B199"
	uiBad    = "#EB5757"
)

// themeCSS defines the local variables on the widget root <p>-w, plus the
// shared look of inputs and selects. Goes first inside a <style> block.
func themeCSS(p string) string {
	return fmt.Sprintf(`.%[1]s-w{--bb-fg:rgb(var(--color-black-900));--bb-muted:rgb(var(--color-black-800));--bb-surface:rgb(var(--color-white-200));--bb-field:rgb(var(--color-white-100));--bb-border:rgb(var(--color-white-400));color-scheme:light;font-family:inherit}
.dark .%[1]s-w{--bb-fg:rgb(var(--color-white-100));--bb-muted:rgb(var(--color-black-700));--bb-surface:rgb(var(--color-navy-700));--bb-field:rgb(var(--color-navy-800));--bb-border:rgb(var(--color-navy-600));color-scheme:dark}
.%[1]s-w input,.%[1]s-w select{outline:none}
.%[1]s-w input:focus,.%[1]s-w select:focus{border-color:#27B199}
.%[1]s-w select option{background:var(--bb-field);color:var(--bb-fg)}
`, p)
}

// RulesTestInput drives the panel. browser carries the field's own value by
// the html= convention; pr comes from the named input in the markup.
type RulesTestInput struct {
	Browser string `wick:"desc=Current field value, supplied by the config UI."`
	PR      string `wick:"key=pr;desc=Pull request to test: its URL (https://bitbucket.org/workspace/repo/pull-requests/12) or repo_slug/12."`
}

func rulesTestPanel(c *connector.Ctx) (any, error) {
	// Restore the last input: the manager re-runs this op on every re-mount.
	return map[string]any{"html": renderRulesTest(strings.TrimSpace(c.Cfg("rules_test_pr")), nil, "")}, nil
}

func rulesTestRun(c *connector.Ctx) (any, error) {
	raw := strings.TrimSpace(c.Input("pr"))
	fields := map[string]string{"rules_test_pr": raw}
	workspace, repo, id, err := parsePRRef(raw, strings.TrimSpace(c.Cfg("default_workspace")))
	if err != nil {
		return map[string]any{"html": renderRulesTest(raw, nil, err.Error()), "fields": fields}, nil
	}
	v, matched, err := checkMergeRules(c, workspace, repo, id)
	if err != nil {
		return map[string]any{"html": renderRulesTest(raw, nil, err.Error()), "fields": fields}, nil
	}
	if !matched {
		v.Checks = []ruleCheck{{Name: "Matching rule", OK: true, Detail: "no merge_rules row matches " + v.Repo + ", so merge_pull_request merges it without checks"}}
	}
	return map[string]any{"html": renderRulesTest(raw, &v, ""), "fields": fields}, nil
}

// parsePRRef accepts a Bitbucket PR URL, workspace/repo/id, or repo/id (the
// workspace then comes from default_workspace).
func parsePRRef(raw, defaultWorkspace string) (workspace, repo string, id int, err error) {
	if raw == "" {
		return "", "", 0, fmt.Errorf("paste a pull request URL or repo_slug/id")
	}
	ref := raw
	if u, perr := url.Parse(raw); perr == nil && u.Host != "" {
		ref = strings.Replace(strings.Trim(u.Path, "/"), "/pull-requests/", "/", 1)
	}
	parts := strings.Split(strings.Trim(ref, "/"), "/")
	switch len(parts) {
	case 2:
		workspace, repo = defaultWorkspace, parts[0]
	case 3:
		workspace, repo = parts[0], parts[1]
	default:
		return "", "", 0, fmt.Errorf("%q is not a pull request URL or repo_slug/id", raw)
	}
	id, _ = strconv.Atoi(parts[len(parts)-1])
	if id <= 0 || repo == "" {
		return "", "", 0, fmt.Errorf("%q is not a pull request URL or repo_slug/id", raw)
	}
	if workspace == "" {
		return "", "", 0, fmt.Errorf("no workspace in %q and default_workspace is empty", raw)
	}
	return workspace, repo, id, nil
}

func renderRulesTest(prVal string, v *mergeVerdict, errMsg string) string {
	const p = "bbrt"
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString("<style>" + themeCSS(p))
	fmt.Fprintf(&b, `.%[1]s-w{color:%[2]s;font-size:13px;line-height:1.4}
.%[1]s-form{display:flex;gap:8px;margin-bottom:8px;flex-wrap:wrap}
.%[1]s-in{flex:1 1 320px;box-sizing:border-box;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px;height:32px;padding:0 8px;border:1px solid %[3]s;border-radius:8px;background:%[7]s;color:%[2]s}
.%[1]s-btn{cursor:pointer;font-family:inherit;font-size:13px;font-weight:500;height:32px;padding:0 16px;border-radius:8px;border:1px solid %[5]s;background:%[5]s;color:#fff}
.%[1]s-note{font-size:12px;color:var(--bb-muted)}
.%[1]s-res{margin-top:12px;border:1px solid %[3]s;border-radius:12px;background:%[4]s;padding:4px 16px}
.%[1]s-row{display:flex;gap:12px;padding:12px 0;border-top:1px solid %[3]s;align-items:flex-start}
.%[1]s-row:first-of-type{border-top:0}
.%[1]s-badge{flex:0 0 auto;width:20px;height:20px;border-radius:50%%;display:flex;align-items:center;justify-content:center;font-size:11px;color:#fff;margin-top:1px}
.%[1]s-ok{background:#288C7A}
.%[1]s-fail{background:%[6]s}
.%[1]s-name{font-weight:500;font-size:13px}
.%[1]s-detail{font-size:12px;color:var(--bb-muted);margin-top:2px}
.%[1]s-sum{display:flex;gap:8px;align-items:center;font-weight:600;font-size:13px;padding:12px 0 8px}
.%[1]s-pill{font-size:11px;font-weight:600;padding:2px 8px;border-radius:999px;color:#fff}
.%[1]s-err{margin-top:8px;font-size:13px;padding:8px 12px;border-radius:8px;background:%[7]s;border:1px solid %[6]s}
</style><div class="%[1]s-w">`, p, uiText, uiBorder, uiPanel, uiOK, uiBad, uiSunken)

	// type="button": a bare <button> submits the surrounding config form and wipes it.
	fmt.Fprintf(&b, `<div class="%[1]s-form"><input class="%[1]s-in" name="pr" placeholder="https://bitbucket.org/workspace/repo/pull-requests/12" value="%[2]s"/>`+
		`<button type="button" class="%[1]s-btn" data-op="rules_test_run">Test rules</button></div>`, p, esc(prVal))
	fmt.Fprintf(&b, `<div class="%s-note">Checks the pull request against the <b>saved</b> rules (save first after editing the table). Read-only, nothing is merged.</div>`, p)

	if errMsg != "" {
		fmt.Fprintf(&b, `<div class="%s-err">%s</div>`, p, esc(errMsg))
	}
	if v != nil {
		verdict := "MERGE ALLOWED"
		if !v.Allowed {
			verdict = "MERGE REFUSED"
		}
		rule := v.Rule
		if rule == "" {
			rule = "none"
		}
		pill := "#288C7A"
		if !v.Allowed {
			pill = uiBad
		}
		fmt.Fprintf(&b, `<div class="%[1]s-res"><div class="%[1]s-sum"><span class="%[1]s-pill" style="background:%[2]s">%[3]s</span><span>%[4]s</span><span class="%[1]s-note">rule: %[5]s</span></div>`,
			p, pill, verdict, esc(v.Repo), esc(rule))
		for _, ch := range v.Checks {
			cls, mark := "ok", "✓"
			if !ch.OK {
				cls, mark = "fail", "✕"
			}
			fmt.Fprintf(&b, `<div class="%[1]s-row"><span class="%[1]s-badge %[1]s-%[2]s">%[3]s</span><div><div class="%[1]s-name">%[4]s</div><div class="%[1]s-detail">%[5]s</div></div></div>`,
				p, cls, mark, esc(ch.Name), esc(ch.Detail))
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}
