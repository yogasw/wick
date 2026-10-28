// rules.go: the merge_rules guard merge_merge_request applies before every
// merge, same model as the Bitbucket connector. A row names a project (path
// or glob), allows or denies merging it, and for allow sets how many
// approvals the MR needs and who must have approved when they are on it.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// defaultMergeRules: one approval everywhere, and no merging of any helm
// repository (deploy config is only watched, never merged from here).
const defaultMergeRules = `[{"repo":"*","mode":"allow","min_approvals":"1","required_approvers":""},{"repo":"*helm*","mode":"deny","min_approvals":"","required_approvers":""}]`

// savedRules is merge_rules, or the defaults when the instance has never
// saved any: an instance created before merge rules existed still gets the
// guard rail instead of merging unchecked.
func savedRules(c *connector.Ctx) string {
	if raw := strings.TrimSpace(c.Cfg("merge_rules")); raw != "" {
		return raw
	}
	return defaultMergeRules
}

// mergeRule is one row of the merge_rules config. Repo is a project path
// (group/sub/repo) or its last segment, either of which may be a glob.
type mergeRule struct {
	Repo              string
	Mode              string
	MinApprovals      int
	RequiredApprovers []string
}

// parseMergeRules reads the merge_rules kvlist (a JSON array of string-keyed
// rows). A row that cannot be read is an error rather than a skip: a typo in
// a guard rail must stop merges, not quietly lift the rule.
func parseMergeRules(raw string) ([]mergeRule, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil, nil
	}
	var rows []map[string]string
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil, fmt.Errorf("merge_rules is not readable: %w", err)
	}
	out := make([]mergeRule, 0, len(rows))
	for i, r := range rows {
		repo := strings.Trim(strings.TrimSpace(r["repo"]), "/")
		if repo == "" {
			continue
		}
		rule := mergeRule{Repo: repo, Mode: strings.ToLower(strings.TrimSpace(r["mode"]))}
		if rule.Mode == "" {
			rule.Mode = "allow"
		}
		if rule.Mode != "allow" && rule.Mode != "deny" {
			return nil, fmt.Errorf("merge_rules row %d (%s): mode must be allow or deny", i+1, repo)
		}
		if _, err := path.Match(repo, ""); err != nil {
			return nil, fmt.Errorf("merge_rules row %d: repo %q is not a valid glob", i+1, repo)
		}
		if v := strings.TrimSpace(r["min_approvals"]); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("merge_rules row %d (%s): min_approvals must be a whole number", i+1, repo)
			}
			rule.MinApprovals = n
		}
		for _, a := range strings.FieldsFunc(r["required_approvers"], func(c rune) bool { return c == ',' || c == '\n' || c == ' ' }) {
			if a = strings.TrimPrefix(strings.TrimSpace(a), "@"); a != "" {
				rule.RequiredApprovers = append(rule.RequiredApprovers, a)
			}
		}
		out = append(out, rule)
	}
	return out, nil
}

// matchMergeRule picks the rule for a project path. The most specific row
// wins: the most literal (non-*) characters, then the fewest *, then the
// earlier row. So "*helm*" beats "*", and "group/app" beats "*helm*".
func matchMergeRule(rules []mergeRule, project string) (mergeRule, bool) {
	project = strings.ToLower(strings.Trim(project, "/"))
	slug := project
	if i := strings.LastIndex(project, "/"); i >= 0 {
		slug = project[i+1:]
	}
	best, found := mergeRule{}, false
	for _, r := range rules {
		pattern := strings.ToLower(r.Repo)
		okSlug, _ := path.Match(pattern, slug)
		okFull, _ := path.Match(pattern, project)
		if !okSlug && !okFull {
			continue
		}
		if !found || moreSpecific(r.Repo, best.Repo) {
			best, found = r, true
		}
	}
	return best, found
}

func moreSpecific(a, b string) bool {
	la, lb := len(strings.ReplaceAll(a, "*", "")), len(strings.ReplaceAll(b, "*", ""))
	if la != lb {
		return la > lb
	}
	return strings.Count(a, "*") < strings.Count(b, "*")
}

// mergeVerdict is what the rule check reports, both when it refuses a merge
// and in the config page's test panel.
type mergeVerdict struct {
	Repo      string      `json:"repo"`
	Rule      string      `json:"rule"`
	Allowed   bool        `json:"allowed"`
	Approvals int         `json:"approvals"`
	Approved  []string    `json:"approved_by"`
	Checks    []ruleCheck `json:"checks"`
}

type ruleCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func (v *mergeVerdict) check(name string, ok bool, detail string) {
	v.Checks = append(v.Checks, ruleCheck{Name: name, OK: ok, Detail: detail})
}

func (v mergeVerdict) reasons() []string {
	var out []string
	for _, c := range v.Checks {
		if !c.OK {
			out = append(out, c.Detail)
		}
	}
	return out
}

// glUser is a GitLab user as the rules see one.
type glUser struct {
	ID       string
	Username string
	Name     string
}

func userFrom(m map[string]any) (glUser, bool) {
	if m == nil {
		return glUser{}, false
	}
	u := glUser{Username: str(m["username"]), Name: str(m["name"])}
	if id, ok := m["id"].(float64); ok {
		u.ID = strconv.Itoa(int(id))
	}
	return u, u.Username != "" || u.ID != ""
}

func (u glUser) label() string {
	if u.Name != "" {
		return u.Name
	}
	return "@" + u.Username
}

// evaluateMergeRule checks a merge request against rule. mr is GitLab's MR
// JSON, approvers the users in approved_by of /approvals, people everyone
// else on the MR (author, assignees, reviewers, participants).
func evaluateMergeRule(rule mergeRule, project string, mr map[string]any, approvers, people []glUser) mergeVerdict {
	v := mergeVerdict{Repo: project, Rule: rule.Repo}
	if rule.Mode == "deny" {
		v.check("Repository allows merge", false, fmt.Sprintf("merging %s through this connector is denied by merge_rules (row %q)", project, rule.Repo))
		return v
	}
	v.check("Repository allows merge", true, fmt.Sprintf("row %q, mode allow", rule.Repo))
	if state := str(mr["state"]); state == "opened" {
		v.check("Merge request is open", true, "state opened")
	} else {
		v.check("Merge request is open", false, "merge request is "+state+", not opened")
	}

	approved := map[string]bool{}
	for _, u := range approvers {
		approved[strings.ToLower(u.Username)] = true
		v.Approvals++
		v.Approved = append(v.Approved, u.label())
	}
	who := "nobody"
	if len(v.Approved) > 0 {
		who = strings.Join(v.Approved, ", ")
	}
	v.check(fmt.Sprintf("At least %d approval(s)", rule.MinApprovals), v.Approvals >= rule.MinApprovals,
		fmt.Sprintf("has %d, approved by %s", v.Approvals, who))

	everyone := append(append([]glUser{}, approvers...), people...)
	for _, want := range rule.RequiredApprovers {
		w := strings.ToLower(want)
		var hit *glUser
		for i := range everyone {
			u := everyone[i]
			if strings.ToLower(u.Username) == w || u.ID == w {
				hit = &u
				break
			}
		}
		name := "Required approver @" + want
		switch {
		case hit == nil:
			v.check(name, true, "@"+want+" is not on this merge request, so not required")
		case approved[strings.ToLower(hit.Username)]:
			v.check(name, true, hit.label()+" approved")
		default:
			v.check(name, false, hit.label()+" is on this merge request and has not approved yet")
		}
	}
	v.Allowed = len(v.reasons()) == 0
	return v
}

func errMergeRefused(v mergeVerdict) error {
	return errors.New("merge refused by merge_rules: " + strings.Join(v.reasons(), "; "))
}

// mrProjectPath is the MR's real project path, taken from its web_url, so a
// rule matches the same whether the caller passed a numeric id or a path.
func mrProjectPath(mr map[string]any, fallback string) string {
	if u, err := url.Parse(str(mr["web_url"])); err == nil {
		if p, _, ok := strings.Cut(strings.Trim(u.Path, "/"), "/-/merge_requests/"); ok && p != "" {
			return p
		}
	}
	return fallback
}

// checkMergeRules reads the MR and judges it against merge_rules. matched is
// false when no row covers the project: the merge then runs unchecked.
func checkMergeRules(c *connector.Ctx, project string, iid int) (v mergeVerdict, matched bool, err error) {
	rules, err := parseMergeRules(savedRules(c))
	if err != nil {
		return mergeVerdict{}, false, err
	}
	mp := fmt.Sprintf("/projects/%s/merge_requests/%d", url.PathEscape(project), iid)
	raw, err := doJSON(c, http.MethodGet, apiURL(c, mp, nil), nil)
	if err != nil {
		// A deny row needs no MR to refuse; anything else does.
		if rule, ok := matchMergeRule(rules, project); ok && rule.Mode == "deny" {
			return evaluateMergeRule(rule, project, nil, nil, nil), true, nil
		}
		return mergeVerdict{}, false, fmt.Errorf("read merge request for merge_rules: %w", err)
	}
	mr, _ := raw.(map[string]any)
	full := mrProjectPath(mr, project)
	rule, ok := matchMergeRule(rules, full)
	if !ok {
		return mergeVerdict{Repo: full, Allowed: true}, false, nil
	}
	if rule.Mode == "deny" {
		return evaluateMergeRule(rule, full, mr, nil, nil), true, nil
	}
	ap, err := doJSON(c, http.MethodGet, apiURL(c, mp+"/approvals", nil), nil)
	if err != nil {
		return mergeVerdict{}, true, fmt.Errorf("read approvals for merge_rules: %w", err)
	}
	var approvers []glUser
	if m, ok := ap.(map[string]any); ok {
		list, _ := m["approved_by"].([]any)
		for _, item := range list {
			e, _ := item.(map[string]any)
			u, _ := e["user"].(map[string]any)
			if gu, ok := userFrom(u); ok {
				approvers = append(approvers, gu)
			}
		}
	}
	var people []glUser
	if a, _ := mr["author"].(map[string]any); a != nil {
		if gu, ok := userFrom(a); ok {
			people = append(people, gu)
		}
	}
	for _, key := range []string{"assignees", "reviewers"} {
		list, _ := mr[key].([]any)
		for _, item := range list {
			if gu, ok := userFrom(asMap(item)); ok {
				people = append(people, gu)
			}
		}
	}
	// Participants (commenters) are best effort: a token that cannot read
	// them still gets reviewers and assignees checked.
	if pv, err := doJSON(c, http.MethodGet, apiURL(c, mp+"/participants", nil), nil); err == nil {
		list, _ := pv.([]any)
		for _, item := range list {
			if gu, ok := userFrom(asMap(item)); ok {
				people = append(people, gu)
			}
		}
	}
	return evaluateMergeRule(rule, full, mr, approvers, people), true, nil
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}
