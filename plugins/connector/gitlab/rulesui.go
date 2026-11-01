// rulesui.go implements the "Test merge rules" panel on the config page:
// paste a merge request, see the checklist merge_merge_request would apply
// to it. It runs the same checkMergeRules the merge op runs, against the
// SAVED rules, and never merges.
package main

import (
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// Theme tokens, same scheme as the Bitbucket connector: wick's theme
// variables hold bare RGB triplets, dark mode is a "dark" class on <html>.
const (
	uiText   = "var(--gl-fg)"
	uiBorder = "var(--gl-border)"
	uiPanel  = "var(--gl-surface)"
	uiSunken = "var(--gl-field)"
	uiOK     = "#27B199"
	uiBad    = "#EB5757"
)

func themeCSS(p string) string {
	return fmt.Sprintf(`.%[1]s-w{--gl-fg:rgb(var(--color-black-900));--gl-muted:rgb(var(--color-black-800));--gl-surface:rgb(var(--color-white-200));--gl-field:rgb(var(--color-white-100));--gl-border:rgb(var(--color-white-400));color-scheme:light;font-family:inherit}
.dark .%[1]s-w{--gl-fg:rgb(var(--color-white-100));--gl-muted:rgb(var(--color-black-700));--gl-surface:rgb(var(--color-navy-700));--gl-field:rgb(var(--color-navy-800));--gl-border:rgb(var(--color-navy-600));color-scheme:dark}
.%[1]s-w input,.%[1]s-w select{outline:none}
.%[1]s-w input:focus,.%[1]s-w select:focus{border-color:#27B199}
.%[1]s-w select option{background:var(--gl-field);color:var(--gl-fg)}
`, p)
}

// RulesTestInput drives the panel. mr comes from the named input.
type RulesTestInput struct {
	Browser string `wick:"desc=Current field value, supplied by the config UI."`
	MR      string `wick:"key=mr;desc=Merge request to test: its URL (https://gitlab.example.com/group/repo/-/merge_requests/12), group/repo!12, or !12 for default_project."`
}

func rulesTestPanel(c *connector.Ctx) (any, error) {
	// Restore the last input: the manager re-runs this op on every re-mount.
	return map[string]any{"html": renderRulesTest(strings.TrimSpace(c.Cfg("rules_test_mr")), nil, "")}, nil
}

func rulesTestRun(c *connector.Ctx) (any, error) {
	raw := strings.TrimSpace(c.Input("mr"))
	fields := map[string]string{"rules_test_mr": raw}
	project, iid, err := parseMRRef(raw, strings.Trim(strings.TrimSpace(c.Cfg("default_project")), "/"))
	if err != nil {
		return map[string]any{"html": renderRulesTest(raw, nil, err.Error()), "fields": fields}, nil
	}
	v, matched, err := checkMergeRules(c, project, iid)
	if err != nil {
		return map[string]any{"html": renderRulesTest(raw, nil, err.Error()), "fields": fields}, nil
	}
	if !matched {
		v.Checks = []ruleCheck{{Name: "Matching rule", OK: true, Detail: "no merge_rules row matches " + v.Repo + ", so merge_merge_request merges it without checks"}}
	}
	return map[string]any{"html": renderRulesTest(raw, &v, ""), "fields": fields}, nil
}

// parseMRRef accepts an MR URL, group/repo!12, group/repo/12, or !12 / 12
// (the project then comes from default_project).
func parseMRRef(raw, defaultProject string) (project string, iid int, err error) {
	bad := fmt.Errorf("%q is not a merge request URL, group/repo!id or !id", raw)
	if raw == "" {
		return "", 0, fmt.Errorf("paste a merge request URL, group/repo!id or !id")
	}
	ref := raw
	if u, perr := url.Parse(raw); perr == nil && u.Host != "" {
		p, rest, ok := strings.Cut(strings.Trim(u.Path, "/"), "/-/merge_requests/")
		if !ok {
			return "", 0, bad
		}
		ref = p + "!" + strings.SplitN(rest, "/", 2)[0]
	}
	if p, n, ok := strings.Cut(ref, "!"); ok {
		project, ref = strings.Trim(p, "/"), n
	} else if i := strings.LastIndex(ref, "/"); i >= 0 {
		project, ref = strings.Trim(ref[:i], "/"), ref[i+1:]
	}
	iid, _ = strconv.Atoi(strings.TrimSpace(ref))
	if iid <= 0 {
		return "", 0, bad
	}
	if project == "" {
		project = defaultProject
	}
	if project == "" {
		return "", 0, fmt.Errorf("no project in %q and default_project is empty", raw)
	}
	return project, iid, nil
}

func renderRulesTest(mrVal string, v *mergeVerdict, errMsg string) string {
	const p = "glrt"
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString("<style>" + themeCSS(p))
	fmt.Fprintf(&b, `.%[1]s-w{color:%[2]s;font-size:13px;line-height:1.4}
.%[1]s-form{display:flex;gap:8px;margin-bottom:8px;flex-wrap:wrap}
.%[1]s-in{flex:1 1 320px;box-sizing:border-box;font-family:ui-monospace,SFMono-Regular,Menlo,monospace;font-size:12px;height:32px;padding:0 8px;border:1px solid %[3]s;border-radius:8px;background:%[7]s;color:%[2]s}
.%[1]s-btn{cursor:pointer;font-family:inherit;font-size:13px;font-weight:500;height:32px;padding:0 16px;border-radius:8px;border:1px solid %[5]s;background:%[5]s;color:#fff}
.%[1]s-note{font-size:12px;color:var(--gl-muted)}
.%[1]s-res{margin-top:12px;border:1px solid %[3]s;border-radius:12px;background:%[4]s;padding:4px 16px}
.%[1]s-row{display:flex;gap:12px;padding:12px 0;border-top:1px solid %[3]s;align-items:flex-start}
.%[1]s-row:first-of-type{border-top:0}
.%[1]s-badge{flex:0 0 auto;width:20px;height:20px;border-radius:50%%;display:flex;align-items:center;justify-content:center;font-size:11px;color:#fff;margin-top:1px}
.%[1]s-ok{background:#288C7A}
.%[1]s-fail{background:%[6]s}
.%[1]s-name{font-weight:500;font-size:13px}
.%[1]s-detail{font-size:12px;color:var(--gl-muted);margin-top:2px}
.%[1]s-sum{display:flex;gap:8px;align-items:center;font-weight:600;font-size:13px;padding:12px 0 8px}
.%[1]s-pill{font-size:11px;font-weight:600;padding:2px 8px;border-radius:999px;color:#fff}
.%[1]s-err{margin-top:8px;font-size:13px;padding:8px 12px;border-radius:8px;background:%[7]s;border:1px solid %[6]s}
</style><div class="%[1]s-w">`, p, uiText, uiBorder, uiPanel, uiOK, uiBad, uiSunken)

	// type="button": a bare <button> submits the surrounding config form and wipes it.
	fmt.Fprintf(&b, `<div class="%[1]s-form"><input class="%[1]s-in" name="mr" placeholder="https://gitlab.example.com/group/repo/-/merge_requests/12" value="%[2]s"/>`+
		`<button type="button" class="%[1]s-btn" data-op="rules_test_run">Test rules</button></div>`, p, esc(mrVal))
	fmt.Fprintf(&b, `<div class="%s-note">Checks the merge request against the <b>saved</b> rules (save first after editing). Read-only, nothing is merged.</div>`, p)

	if errMsg != "" {
		fmt.Fprintf(&b, `<div class="%s-err">%s</div>`, p, esc(errMsg))
	}
	if v != nil {
		verdict, pill := "MERGE ALLOWED", "#288C7A"
		if !v.Allowed {
			verdict, pill = "MERGE REFUSED", uiBad
		}
		rule := v.Rule
		if rule == "" {
			rule = "none"
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
