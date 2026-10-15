package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// mergeRule is one row of the merge_rules config. repo is a slug or a
// workspace/slug, either of which may be a glob ("*", "client-*"). mode deny
// refuses every merge into a matching repo; allow merges once the PR has at
// least min_approvals approvals and every listed required approver who sits on
// the PR (as reviewer or participant) has approved.
type mergeRule struct {
	Repo              string
	Mode              string
	MinApprovals      int
	RequiredApprovers []string
}

// parseMergeRules reads the merge_rules kvlist (a JSON array of string-keyed
// rows). A row that cannot be read is an error rather than a skip: a typo in a
// guard rail must stop merges, not quietly lift the rule.
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
		repo := strings.TrimSpace(r["repo"])
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
			if a = strings.TrimSpace(a); a != "" {
				rule.RequiredApprovers = append(rule.RequiredApprovers, a)
			}
		}
		out = append(out, rule)
	}
	return out, nil
}

// matchMergeRule picks the rule for workspace/repo. The rule with the fewest
// wildcards wins, so "integration-helm" beats "*"; on a tie the earlier row wins.
func matchMergeRule(rules []mergeRule, workspace, repo string) (mergeRule, bool) {
	full := workspace + "/" + repo
	best, found := mergeRule{}, false
	for _, r := range rules {
		pattern := strings.ToLower(r.Repo)
		okSlug, _ := path.Match(pattern, strings.ToLower(repo))
		okFull, _ := path.Match(pattern, strings.ToLower(full))
		if !okSlug && !okFull {
			continue
		}
		if !found || strings.Count(r.Repo, "*") < strings.Count(best.Repo, "*") {
			best, found = r, true
		}
	}
	return best, found
}

// mergeVerdict is what the rule check reports, both when it refuses a merge
// and in the config page's test panel. Checks lists every rule that was
// evaluated, passed or not, so the panel can show the whole checklist.
type mergeVerdict struct {
	Repo      string      `json:"repo"`
	Rule      string      `json:"rule"`
	Allowed   bool        `json:"allowed"`
	Approvals int         `json:"approvals"`
	Approved  []string    `json:"approved_by"`
	Checks    []ruleCheck `json:"checks"`
}

// ruleCheck is one line of the checklist.
type ruleCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func (v *mergeVerdict) check(name string, ok bool, detail string) {
	v.Checks = append(v.Checks, ruleCheck{Name: name, OK: ok, Detail: detail})
}

// reasons lists the details of every failed check.
func (v mergeVerdict) reasons() []string {
	var out []string
	for _, c := range v.Checks {
		if !c.OK {
			out = append(out, c.Detail)
		}
	}
	return out
}

// evaluateMergeRule checks a pull request (the JSON Bitbucket returns for GET
// pullrequests/{id}) against rule.
func evaluateMergeRule(rule mergeRule, workspace, repo string, pr map[string]any) mergeVerdict {
	v := mergeVerdict{Repo: workspace + "/" + repo, Rule: rule.Repo}
	if rule.Mode == "deny" {
		v.check("Repository allows merge", false, fmt.Sprintf("merging %s through this connector is denied by merge_rules (row %q)", v.Repo, rule.Repo))
		return v
	}
	v.check("Repository allows merge", true, fmt.Sprintf("row %q, mode allow", rule.Repo))
	state, _ := pr["state"].(string)
	if strings.EqualFold(state, "OPEN") {
		v.check("Pull request is open", true, "state OPEN")
	} else {
		v.check("Pull request is open", false, "pull request is "+state+", not OPEN")
	}

	// Everyone on the PR, by every identifier a rule may name them with.
	type person struct {
		ids      []string
		name     string
		approved bool
	}
	people := map[string]*person{}
	var order []string
	add := func(u map[string]any, approved bool) {
		if u == nil {
			return
		}
		uuid, _ := u["uuid"].(string)
		account, _ := u["account_id"].(string)
		key := uuid
		if key == "" {
			key = account
		}
		if key == "" {
			return
		}
		p := people[key]
		if p == nil {
			p = &person{}
			for _, f := range []string{"uuid", "account_id", "nickname", "display_name"} {
				if s, _ := u[f].(string); s != "" {
					p.ids = append(p.ids, strings.ToLower(s))
				}
			}
			p.name, _ = u["display_name"].(string)
			if p.name == "" {
				p.name = key
			}
			people[key] = p
			order = append(order, key)
		}
		p.approved = p.approved || approved
	}
	if list, ok := pr["reviewers"].([]any); ok {
		for _, item := range list {
			u, _ := item.(map[string]any)
			add(u, false)
		}
	}
	if list, ok := pr["participants"].([]any); ok {
		for _, item := range list {
			p, _ := item.(map[string]any)
			if p == nil {
				continue
			}
			approved, _ := p["approved"].(bool)
			u, _ := p["user"].(map[string]any)
			add(u, approved)
		}
	}
	for _, key := range order {
		if p := people[key]; p.approved {
			v.Approvals++
			v.Approved = append(v.Approved, p.name)
		}
	}

	who := "nobody"
	if len(v.Approved) > 0 {
		who = strings.Join(v.Approved, ", ")
	}
	v.check(fmt.Sprintf("At least %d approval(s)", rule.MinApprovals), v.Approvals >= rule.MinApprovals,
		fmt.Sprintf("has %d, approved by %s", v.Approvals, who))
	for _, want := range rule.RequiredApprovers {
		w := strings.ToLower(want)
		var hit *person
		for _, key := range order {
			for _, id := range people[key].ids {
				if id == w {
					hit = people[key]
				}
			}
		}
		name := "Required approver " + want
		switch {
		case hit == nil:
			v.check(name, true, want+" is not on this pull request, so not required")
		case hit.approved:
			v.check(name, true, hit.name+" approved")
		default:
			v.check(name, false, hit.name+" is on this pull request and has not approved yet")
		}
	}
	v.Allowed = len(v.reasons()) == 0
	return v
}

// errMergeRefused wraps the verdict so the caller sees every reason at once.
func errMergeRefused(v mergeVerdict) error {
	return errors.New("merge refused by merge_rules: " + strings.Join(v.reasons(), "; "))
}

// checkMergeRules fetches the pull request and judges it against the
// merge_rules config. matched is false when merge_rules is empty or no row
// covers the repository: the merge then runs unchecked, as it always has.
func checkMergeRules(c *connector.Ctx, workspace, repo string, prID int) (v mergeVerdict, matched bool, err error) {
	rules, err := parseMergeRules(c.Cfg("merge_rules"))
	if err != nil {
		return mergeVerdict{}, false, err
	}
	rule, ok := matchMergeRule(rules, workspace, repo)
	if !ok {
		return mergeVerdict{Repo: workspace + "/" + repo, Allowed: true}, false, nil
	}
	if rule.Mode == "deny" {
		// No need to read the pull request to refuse it.
		return evaluateMergeRule(rule, workspace, repo, nil), true, nil
	}
	u, err := resourceURL(c, "repositories", workspace, repo, "pullrequests", strconv.Itoa(prID))
	if err != nil {
		return mergeVerdict{}, true, err
	}
	raw, err := fetchJSON(c, requestParams{Method: http.MethodGet, URL: u})
	if err != nil {
		return mergeVerdict{}, true, fmt.Errorf("read pull request for merge_rules: %w", err)
	}
	pr, _ := raw.(map[string]any)
	return evaluateMergeRule(rule, workspace, repo, pr), true, nil
}
