package main

// settings.go — read-only views of a repository's protection settings
// (rulesets, branch rules, deployment environments) plus validate_repo_setup,
// which checks them against the agreed release setup.
//
// Every op here is a GET. GitHub answers 403 or 404 when the token cannot
// read a setting, so those two codes are rewritten into a message that names
// the permission. Bypass lists need more: GitHub returns bypass_actors only
// to repository admins, and silently drops the field for everyone else.

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// ── inputs ───────────────────────────────────────────────────────────

// ListRulesetsInput lists the rulesets of a repository.
type ListRulesetsInput struct {
	Owner           string `wick:"required;desc=Repository owner."`
	Repo            string `wick:"required;desc=Repository name."`
	IncludesParents bool   `wick:"desc=Whether to also return rulesets inherited from the organisation. Omitted: GitHub's default (true)."`
}

// GetRulesetInput fetches one ruleset with its conditions, rules and bypass list.
type GetRulesetInput struct {
	Owner string `wick:"required;desc=Repository owner."`
	Repo  string `wick:"required;desc=Repository name."`
	ID    int    `wick:"required;desc=Ruleset ID, from list_rulesets."`
}

// GetBranchRulesInput lists the rules in force on one branch.
type GetBranchRulesInput struct {
	Owner  string `wick:"required;desc=Repository owner."`
	Repo   string `wick:"required;desc=Repository name."`
	Branch string `wick:"required;desc=Branch name. Example: master"`
}

// ListEnvironmentsInput lists the deployment environments of a repository.
type ListEnvironmentsInput struct {
	Owner string `wick:"required;desc=Repository owner."`
	Repo  string `wick:"required;desc=Repository name."`
}

// GetEnvironmentInput fetches one deployment environment.
type GetEnvironmentInput struct {
	Owner string `wick:"required;desc=Repository owner."`
	Repo  string `wick:"required;desc=Repository name."`
	Name  string `wick:"required;desc=Environment name. Example: release-approval"`
}

// ValidateRepoSetupInput names the repository to check. Both fall back to
// the instance's default_owner / default_repo.
type ValidateRepoSetupInput struct {
	Owner string `wick:"desc=Repository owner. Default: the instance's default_owner."`
	Repo  string `wick:"desc=Repository name. Default: the instance's default_repo."`
}

// ── permission errors ────────────────────────────────────────────────

// settingsReadHint is appended to 403/404 answers from the settings endpoints.
const settingsReadHint = "the token needs read access to the repository's settings: a classic token with the `repo` scope, " +
	"or a fine-grained token with Administration: Read and Environments: Read on this repository. " +
	"Bypass lists (bypass_actors) are only returned to repo admins " +
	"(404 is also what GitHub returns when the repository or item does not exist or is not visible to the token)"

// explainSettingsErr rewrites a 403/404 into a message naming the missing
// permission; any other error passes through unchanged.
func explainSettingsErr(err error) error {
	var ae *apiError
	if errors.As(err, &ae) && (ae.Status == 403 || ae.Status == 404) {
		return fmt.Errorf("%w — %s", ae, settingsReadHint)
	}
	return err
}

// settingsOwnerRepo is requireOwnerRepo plus a name check, so neither value
// can reshape the URL path.
func settingsOwnerRepo(c *connector.Ctx) (string, string, error) {
	owner, repo, err := requireOwnerRepo(c)
	if err != nil {
		return "", "", err
	}
	return owner, repo, checkRepoName(owner, repo)
}

func settingsGet(c *connector.Ctx, p string) (any, error) {
	v, err := doRequest(c, "GET", buildURL(c, p), nil)
	return v, explainSettingsErr(err)
}

// ── handlers ─────────────────────────────────────────────────────────

func listRulesets(c *connector.Ctx) (any, error) {
	owner, repo, err := settingsOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	p := fmt.Sprintf("/repos/%s/%s/rulesets?per_page=100", owner, repo)
	if strings.TrimSpace(c.Input("includes_parents")) != "" {
		p += "&includes_parents=" + strconv.FormatBool(c.InputBool("includes_parents"))
	}
	v, err := settingsGet(c, p)
	if err != nil {
		return nil, err
	}
	arr, _ := v.([]any)
	out := make([]any, 0, len(arr))
	for _, it := range arr {
		m, _ := it.(map[string]any)
		out = append(out, pick(m, "id", "name", "target", "enforcement", "source_type", "source"))
	}
	return out, nil
}

func getRuleset(c *connector.Ctx) (any, error) {
	owner, repo, err := settingsOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	id := c.InputInt("id")
	if id <= 0 {
		return nil, fmt.Errorf("id is required")
	}
	v, err := settingsGet(c, fmt.Sprintf("/repos/%s/%s/rulesets/%d", owner, repo, id))
	if err != nil {
		return nil, err
	}
	m, _ := v.(map[string]any)
	return pick(m, "id", "name", "target", "enforcement", "source_type", "source",
		"conditions", "rules", "bypass_actors", "current_user_can_bypass"), nil
}

func getBranchRules(c *connector.Ctx) (any, error) {
	owner, repo, err := settingsOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	branch := strings.TrimSpace(c.Input("branch"))
	if branch == "" {
		return nil, fmt.Errorf("branch is required")
	}
	return settingsGet(c, fmt.Sprintf("/repos/%s/%s/rules/branches/%s?per_page=100", owner, repo, url.PathEscape(branch)))
}

func listEnvironments(c *connector.Ctx) (any, error) {
	owner, repo, err := settingsOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	v, err := settingsGet(c, fmt.Sprintf("/repos/%s/%s/environments?per_page=100", owner, repo))
	if err != nil {
		return nil, err
	}
	m, _ := v.(map[string]any)
	envs, _ := m["environments"].([]any)
	out := make([]any, 0, len(envs))
	for _, e := range envs {
		em, _ := e.(map[string]any)
		out = append(out, trimEnvironment(em))
	}
	return map[string]any{"total_count": m["total_count"], "environments": out}, nil
}

func getEnvironment(c *connector.Ctx) (any, error) {
	owner, repo, err := settingsOwnerRepo(c)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(c.Input("name"))
	if name == "" {
		return nil, fmt.Errorf("name is required")
	}
	v, err := settingsGet(c, fmt.Sprintf("/repos/%s/%s/environments/%s", owner, repo, url.PathEscape(name)))
	if err != nil {
		return nil, err
	}
	m, _ := v.(map[string]any)
	return trimEnvironment(m), nil
}

// trimEnvironment keeps what matters for review: name, protection rules
// (reviewers reduced to type + login/slug) and the branch policy.
func trimEnvironment(m map[string]any) map[string]any {
	out := pick(m, "id", "name", "can_admins_bypass", "deployment_branch_policy")
	rules, _ := m["protection_rules"].([]any)
	trimmed := make([]any, 0, len(rules))
	for _, r := range rules {
		rm, _ := r.(map[string]any)
		tr := pick(rm, "type", "prevent_self_review", "wait_timer")
		if revs, ok := rm["reviewers"].([]any); ok {
			list := make([]any, 0, len(revs))
			for _, rv := range revs {
				rvm, _ := rv.(map[string]any)
				who, _ := rvm["reviewer"].(map[string]any)
				list = append(list, map[string]any{"type": rvm["type"], "name": firstNonEmpty(str(who, "login"), str(who, "slug"), str(who, "name"))})
			}
			tr["reviewers"] = list
		}
		trimmed = append(trimmed, tr)
	}
	out["protection_rules"] = trimmed
	return out
}

// ── validate_repo_setup ──────────────────────────────────────────────

// Check statuses. warn = recommended, or not required yet.
const (
	statusPass = "pass"
	statusFail = "fail"
	statusWarn = "warn"
)

// setupCheck is one line of the validation report.
type setupCheck struct {
	Group  string `json:"group"`
	Name   string `json:"name"`
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// setupReport is what validate_repo_setup returns and the panel renders.
type setupReport struct {
	Owner  string       `json:"owner"`
	Repo   string       `json:"repo"`
	Passed int          `json:"passed"`
	Failed int          `json:"failed"`
	Warned int          `json:"warned"`
	Checks []setupCheck `json:"checks"`
}

func (r *setupReport) add(group, name, status, reason string) {
	r.Checks = append(r.Checks, setupCheck{Group: group, Name: name, Status: status, Reason: reason})
	switch status {
	case statusPass:
		r.Passed++
	case statusFail:
		r.Failed++
	default:
		r.Warned++
	}
}

const (
	// releaseEnvironment is the environment the release workflow pauses on.
	releaseEnvironment = "release-approval"
	// masterGuardCheck is the required status check P46 adds on master.
	masterGuardCheck = "master-source-guard"
	// adminRoleID is the built-in Repository admin role in bypass_actors.
	adminRoleID = 5
)

// repoNameRe is what GitHub allows in an owner or repository name. Checked
// before either goes into a URL path.
var repoNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

func checkRepoName(owner, repo string) error {
	for _, s := range []string{owner, repo} {
		if !repoNameRe.MatchString(s) || s == "." || s == ".." {
			return fmt.Errorf("invalid owner/repo %q: only letters, digits, '-', '_' and '.' are allowed", s)
		}
	}
	return nil
}

func validateRepoSetup(c *connector.Ctx) (any, error) {
	owner := firstNonEmpty(strings.TrimSpace(c.Input("owner")), strings.TrimSpace(c.Cfg("default_owner")))
	repo := firstNonEmpty(strings.TrimSpace(c.Input("repo")), strings.TrimSpace(c.Cfg("default_repo")))
	if owner == "" || repo == "" {
		return nil, fmt.Errorf("owner and repo are required (or set default_owner and default_repo on the instance)")
	}
	if err := checkRepoName(owner, repo); err != nil {
		return nil, err
	}
	return runSetupValidation(c, owner, repo), nil
}

// runSetupValidation never fails as a whole: an unreadable endpoint becomes
// a failed check with the reason, so one gap never hides the other checks.
func runSetupValidation(c *connector.Ctx, owner, repo string) *setupReport {
	r := &setupReport{Owner: owner, Repo: repo}
	v := &setupValidator{c: c, base: fmt.Sprintf("/repos/%s/%s", owner, repo), rulesets: map[int]map[string]any{}}

	// Token permission probes.
	list, listErr := settingsGet(c, v.base+"/rulesets?per_page=100")
	if listErr != nil {
		r.add("Token", "Can read rulesets", statusFail, listErr.Error())
	} else {
		r.add("Token", "Can read rulesets", statusPass, "GET /rulesets answered.")
	}
	_, envErr := settingsGet(c, v.base+"/environments?per_page=100")
	if envErr != nil {
		r.add("Token", "Can read environments", statusFail, envErr.Error())
	} else {
		r.add("Token", "Can read environments", statusPass, "GET /environments answered.")
	}
	v.checkBypassReadable(r, list, listErr)

	v.checkMaster(r)
	v.checkDevelop(r)
	v.checkTags(r, list, listErr)
	v.checkEnvironment(r, envErr == nil)
	return r
}

type setupValidator struct {
	c        *connector.Ctx
	base     string
	rulesets map[int]map[string]any // full rulesets by id, fetched once
}

// branchRules returns the active rules on a branch, by type. Each rule
// keeps its ruleset_id, so a verdict can be traced to the ruleset that
// enforces it.
func (v *setupValidator) branchRules(branch string) (map[string][]map[string]any, error) {
	raw, err := settingsGet(v.c, v.base+"/rules/branches/"+url.PathEscape(branch)+"?per_page=100")
	if err != nil {
		return nil, err
	}
	arr, _ := raw.([]any)
	byType := map[string][]map[string]any{}
	for _, it := range arr {
		m, _ := it.(map[string]any)
		t := str(m, "type")
		byType[t] = append(byType[t], m)
	}
	return byType, nil
}

// rulesetIDs lists, once each, the rulesets contributing a rule of any of
// the given types; no types means every rule.
func rulesetIDs(rules map[string][]map[string]any, types ...string) []int {
	var ids []int
	seen := map[int]bool{}
	addFrom := func(l []map[string]any) {
		for _, m := range l {
			if id := intAt(m, "ruleset_id"); id > 0 && !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	if len(types) == 0 {
		keys := make([]string, 0, len(rules))
		for k := range rules {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		types = keys
	}
	for _, t := range types {
		addFrom(rules[t])
	}
	sort.Ints(ids)
	return ids
}

func (v *setupValidator) ruleset(id int) (map[string]any, error) {
	if m, ok := v.rulesets[id]; ok {
		return m, nil
	}
	raw, err := settingsGet(v.c, fmt.Sprintf("%s/rulesets/%d", v.base, id))
	if err != nil {
		return nil, err
	}
	m, _ := raw.(map[string]any)
	v.rulesets[id] = m
	return m, nil
}

// checkBypassReadable: GitHub lists rulesets to anyone with read access but
// returns bypass_actors only to repo admins, so the bypass checks below are
// only meaningful when this probe passes.
func (v *setupValidator) checkBypassReadable(r *setupReport, list any, listErr error) {
	const name = "Can read bypass lists"
	if listErr != nil {
		r.add("Token", name, statusFail, "rulesets unreadable")
		return
	}
	arr, _ := list.([]any)
	if len(arr) == 0 {
		r.add("Token", name, statusWarn, "the repository has no ruleset to probe")
		return
	}
	first, _ := arr[0].(map[string]any)
	rs, err := v.ruleset(intAt(first, "id"))
	if err != nil {
		r.add("Token", name, statusFail, err.Error())
		return
	}
	if _, ok := rs["bypass_actors"]; !ok {
		r.add("Token", name, statusFail, fmt.Sprintf("ruleset %q came back without bypass_actors: GitHub only returns it to repo admins, so the admin-bypass checks cannot be confirmed", str(rs, "name")))
		return
	}
	r.add("Token", name, statusPass, fmt.Sprintf("ruleset %q returned bypass_actors", str(rs, "name")))
}

// adminBypassMode returns the bypass_mode Repository admin (or the
// organisation admin) has on a ruleset. listed is false when the ruleset
// came back without bypass_actors.
func adminBypassMode(rs map[string]any) (mode string, found, listed bool) {
	actors, ok := rs["bypass_actors"].([]any)
	if !ok {
		return "", false, false
	}
	for _, a := range actors {
		am, _ := a.(map[string]any)
		if (str(am, "actor_type") == "RepositoryRole" && intAt(am, "actor_id") == adminRoleID) || str(am, "actor_type") == "OrganizationAdmin" {
			return firstNonEmpty(str(am, "bypass_mode"), "always"), true, true
		}
	}
	return "", false, true
}

// adminBypassAll reports whether admin can bypass EVERY one of the given
// rulesets with one of the allowed modes. Bypass is per ruleset, so one
// ruleset without it is enough to block. known is false when a bypass
// list could not be read.
func (v *setupValidator) adminBypassAll(ids []int, modes ...string) (ok, known bool, why string) {
	var good []string
	for _, id := range ids {
		rs, err := v.ruleset(id)
		if err != nil {
			return false, false, err.Error()
		}
		name := firstNonEmpty(str(rs, "name"), strconv.Itoa(id))
		mode, found, listed := adminBypassMode(rs)
		switch {
		case !listed:
			return false, false, fmt.Sprintf("ruleset %q came back without bypass_actors (needs repo admin)", name)
		case !found:
			return false, true, fmt.Sprintf("ruleset %q does not list Repository admin as a bypass actor", name)
		case !hasString(modes, mode):
			return false, true, fmt.Sprintf("ruleset %q lets admin bypass only in %q mode (needs %s)", name, mode, strings.Join(modes, " or "))
		}
		good = append(good, fmt.Sprintf("%q (%s)", name, mode))
	}
	return true, true, "admin bypasses " + strings.Join(good, ", ")
}

// prAgg folds every pull_request rule on a branch the way GitHub enforces
// them together: the highest approval count and only the merge methods all
// rules allow.
type prAgg struct {
	present   bool
	approvals int
	methods   []string
	// approvalIDs are the rulesets whose rule asks for approvals.
	approvalIDs []int
}

func aggregatePR(rules map[string][]map[string]any) prAgg {
	a := prAgg{}
	seen := map[int]bool{}
	for _, rule := range rules["pull_request"] {
		p, _ := rule["parameters"].(map[string]any)
		n := intAt(p, "required_approving_review_count")
		if n > a.approvals {
			a.approvals = n
		}
		if id := intAt(rule, "ruleset_id"); n > 0 && id > 0 && !seen[id] {
			seen[id] = true
			a.approvalIDs = append(a.approvalIDs, id)
		}
		m := mergeMethods(p)
		if !a.present {
			a.methods = m
		} else {
			var keep []string
			for _, x := range a.methods {
				if hasString(m, x) {
					keep = append(keep, x)
				}
			}
			a.methods = keep
		}
		a.present = true
	}
	sort.Ints(a.approvalIDs)
	return a
}

func methodList(m []string) string {
	if len(m) == 0 {
		return "none (the pull_request rules allow no common method)"
	}
	return strings.Join(m, ", ")
}

// mergeMethods is the allowed_merge_methods list; GitHub treats an absent
// list as all three.
func mergeMethods(p map[string]any) []string {
	raw, ok := p["allowed_merge_methods"].([]any)
	if !ok {
		return []string{"merge", "squash", "rebase"}
	}
	out := make([]string, 0, len(raw))
	for _, m := range raw {
		if s, ok := m.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func hasString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// statusCheckContexts lists the required status check names on a branch.
func statusCheckContexts(rules map[string][]map[string]any) []string {
	var out []string
	for _, rule := range rules["required_status_checks"] {
		p, _ := rule["parameters"].(map[string]any)
		checks, _ := p["required_status_checks"].([]any)
		for _, ch := range checks {
			cm, _ := ch.(map[string]any)
			if s := str(cm, "context"); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

func (v *setupValidator) checkMaster(r *setupReport) {
	const g = "master"
	rules, err := v.branchRules("master")
	if err != nil {
		r.add(g, "Ruleset active", statusFail, err.Error())
		return
	}
	if len(rules) == 0 {
		r.add(g, "Ruleset active", statusFail, "no active ruleset rule applies to master")
		return
	}
	r.add(g, "Ruleset active", statusPass, fmt.Sprintf("%d active rule(s) from %d ruleset(s)", countRules(rules), len(rulesetIDs(rules))))

	pr := aggregatePR(rules)
	if !pr.present {
		r.add(g, "Pull request required", statusFail, "no pull_request rule on master")
	} else {
		r.add(g, "Pull request required", statusPass, fmt.Sprintf("%d pull_request rule(s)", len(rules["pull_request"])))
		if pr.approvals == 0 {
			r.add(g, "Approvals 0 or admin bypass", statusPass, "required approvals = 0 (the gate is the environment)")
		} else {
			// Merging goes through the PR API, so pull_request mode is enough.
			ok, known, why := v.adminBypassAll(pr.approvalIDs, "always", "pull_request", "exempt")
			switch {
			case ok:
				r.add(g, "Approvals 0 or admin bypass", statusPass, fmt.Sprintf("required approvals = %d, but %s", pr.approvals, why))
			case !known:
				r.add(g, "Approvals 0 or admin bypass", statusFail, fmt.Sprintf("required approvals = %d and the bypass list is unreadable: %s", pr.approvals, why))
			default:
				r.add(g, "Approvals 0 or admin bypass", statusFail, fmt.Sprintf("required approvals = %d and %s", pr.approvals, why))
			}
		}
		if len(pr.methods) == 1 && pr.methods[0] == "squash" {
			r.add(g, "Squash merge only", statusPass, "allowed merge methods: squash")
		} else {
			r.add(g, "Squash merge only", statusFail, "allowed merge methods: "+methodList(pr.methods))
		}
	}
	if hasString(statusCheckContexts(rules), masterGuardCheck) {
		r.add(g, "Required check "+masterGuardCheck, statusPass, "required status check present")
	} else {
		r.add(g, "Required check "+masterGuardCheck, statusWarn, "not configured yet — added in P46")
	}
	addPresence(r, g, "Force push blocked", rules, "non_fast_forward")
	addPresence(r, g, "Deletion restricted", rules, "deletion")

	// release.yml merges into release with merge_method=merge, so the
	// master ruleset must not reach release and nothing on release may
	// forbid a merge commit.
	rel, relErr := v.branchRules("release")
	if relErr != nil {
		r.add(g, "Not applied to release", statusWarn, "could not read rules for release: "+relErr.Error())
		r.add("release", "Merge commit allowed", statusWarn, "could not read rules for release: "+relErr.Error())
		return
	}
	masterIDs := rulesetIDs(rules, "pull_request", "required_status_checks")
	var shared []string
	for _, id := range rulesetIDs(rel) {
		for _, m := range masterIDs {
			if id == m {
				name := strconv.Itoa(id)
				if rs, err := v.ruleset(id); err == nil {
					name = firstNonEmpty(str(rs, "name"), name)
				}
				shared = append(shared, fmt.Sprintf("%q", name))
			}
		}
	}
	if len(shared) > 0 {
		r.add(g, "Not applied to release", statusFail, "ruleset "+strings.Join(shared, ", ")+" also targets release")
	} else {
		r.add(g, "Not applied to release", statusPass, "no master pull_request/status-check ruleset targets release")
	}
	relPR := aggregatePR(rel)
	switch {
	case len(rel["required_linear_history"]) > 0:
		r.add("release", "Merge commit allowed", statusFail, "required_linear_history is on for release, so release.yml's merge commit is refused")
	case relPR.present && !hasString(relPR.methods, "merge"):
		r.add("release", "Merge commit allowed", statusFail, "pull_request rules on release allow only: "+methodList(relPR.methods))
	default:
		r.add("release", "Merge commit allowed", statusPass, "nothing on release forbids a merge commit")
	}
}

func (v *setupValidator) checkDevelop(r *setupReport) {
	const g = "develop"
	rules, err := v.branchRules("develop")
	if err != nil {
		r.add(g, "Pull request required", statusFail, err.Error())
		return
	}
	pr := aggregatePR(rules)
	if pr.present {
		r.add(g, "Pull request required", statusPass, fmt.Sprintf("%d pull_request rule(s)", len(rules["pull_request"])))
	} else {
		r.add(g, "Pull request required", statusFail, "no pull_request rule on develop")
	}
	if len(rules["required_linear_history"]) > 0 {
		r.add(g, "Linear history off", statusFail, "required_linear_history is on; the master → develop sync needs merge commits")
	} else {
		r.add(g, "Linear history off", statusPass, "no required_linear_history rule")
	}
	switch {
	case !pr.present:
		r.add(g, "Squash not allowed (recommended)", statusWarn, "skipped: no pull_request rule")
	case hasString(pr.methods, "squash"):
		r.add(g, "Squash not allowed (recommended)", statusWarn, "allowed merge methods include squash: "+methodList(pr.methods))
	default:
		r.add(g, "Squash not allowed (recommended)", statusPass, "allowed merge methods: "+methodList(pr.methods))
	}
	addPresence(r, g, "Force push blocked", rules, "non_fast_forward")
	addPresence(r, g, "Deletion restricted", rules, "deletion")

	// sync-develop pushes straight to develop with the admin token, so admin
	// must bypass every ruleset that would refuse a direct push, outside PRs.
	blocking := rulesetIDs(rules, "pull_request", "required_status_checks", "update")
	if len(blocking) == 0 {
		r.add(g, "Admin bypass", statusPass, "no ruleset blocks a direct push")
		return
	}
	ok, known, why := v.adminBypassAll(blocking, "always", "exempt")
	switch {
	case ok:
		r.add(g, "Admin bypass", statusPass, why)
	case !known:
		r.add(g, "Admin bypass", statusFail, "cannot confirm: "+why)
	default:
		r.add(g, "Admin bypass", statusFail, why+"; the direct push by sync-develop will be refused")
	}
}

// tagRefs are the tag shapes the tag ruleset must cover: core v* tags and
// the plugins' <key>/v* tags.
var tagRefs = []struct{ label, ref string }{
	{"v*", "refs/tags/v1.0.0"},
	{"*/v*", "refs/tags/plugin/v1.0.0"},
}

func (v *setupValidator) checkTags(r *setupReport, list any, listErr error) {
	const g = "tags"
	if listErr != nil {
		r.add(g, "Tag ruleset covers v* and */v*", statusFail, "rulesets unreadable: "+listErr.Error())
		return
	}
	arr, _ := list.([]any)
	covered := map[string]string{} // label → ruleset name
	var notes []string
	for _, it := range arr {
		m, _ := it.(map[string]any)
		if str(m, "target") != "tag" {
			continue
		}
		if str(m, "enforcement") != "active" {
			notes = append(notes, fmt.Sprintf("%q is %s", str(m, "name"), str(m, "enforcement")))
			continue
		}
		rs, err := v.ruleset(intAt(m, "id"))
		if err != nil {
			notes = append(notes, err.Error())
			continue
		}
		types := map[string]bool{}
		rl, _ := rs["rules"].([]any)
		for _, x := range rl {
			xm, _ := x.(map[string]any)
			types[str(xm, "type")] = true
		}
		if !types["update"] || !types["deletion"] {
			notes = append(notes, fmt.Sprintf("%q does not block both update and deletion", str(rs, "name")))
			continue
		}
		for _, t := range tagRefs {
			if refMatches(rs, t.ref) {
				covered[t.label] = str(rs, "name")
			}
		}
	}
	for _, t := range tagRefs {
		if name, ok := covered[t.label]; ok {
			r.add(g, "Tags "+t.label+" protected", statusPass, fmt.Sprintf("ruleset %q blocks update and deletion", name))
			continue
		}
		reason := "no active tag ruleset blocking update and deletion covers " + t.label
		if len(notes) > 0 {
			reason += " (" + strings.Join(notes, "; ") + ")"
		}
		r.add(g, "Tags "+t.label+" protected", statusFail, reason)
	}
}

// refMatches applies a ruleset's ref_name include/exclude patterns to ref.
// Patterns are fnmatch-style as on GitHub: `*` does not cross `/`, `**`
// does.
func refMatches(rs map[string]any, ref string) bool {
	cond, _ := rs["conditions"].(map[string]any)
	rn, _ := cond["ref_name"].(map[string]any)
	match := func(key string) bool {
		pats, _ := rn[key].([]any)
		for _, p := range pats {
			s, _ := p.(string)
			if s == "~ALL" || globMatch(s, ref) {
				return true
			}
		}
		return false
	}
	return match("include") && !match("exclude")
}

// globMatch matches ref against a ruleset pattern: `**` = any run of
// characters, `*` = any run without `/`, `?` = one character but `/`.
func globMatch(pattern, ref string) bool {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		switch ch := pattern[i]; {
		case ch == '*' && i+1 < len(pattern) && pattern[i+1] == '*':
			b.WriteString(".*")
			i++
		case ch == '*':
			b.WriteString("[^/]*")
		case ch == '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(ch)))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	return err == nil && re.MatchString(ref)
}

// checkEnvironment: listed says GET /environments answered, so a 404 on
// the environment itself means it does not exist rather than "unreadable".
func (v *setupValidator) checkEnvironment(r *setupReport, listed bool) {
	const g = "environment"
	raw, err := settingsGet(v.c, v.base+"/environments/"+releaseEnvironment)
	if err != nil {
		var ae *apiError
		if listed && errors.As(err, &ae) && ae.Status == 404 {
			r.add(g, releaseEnvironment+" exists", statusFail, "environment "+releaseEnvironment+" does not exist")
		} else {
			r.add(g, releaseEnvironment+" exists", statusFail, err.Error())
		}
		return
	}
	r.add(g, releaseEnvironment+" exists", statusPass, "environment found")
	m, _ := raw.(map[string]any)
	rules, _ := m["protection_rules"].([]any)
	for _, x := range rules {
		xm, _ := x.(map[string]any)
		if str(xm, "type") != "required_reviewers" {
			continue
		}
		revs, _ := xm["reviewers"].([]any)
		if len(revs) == 0 {
			r.add(g, "Required reviewers", statusFail, "required_reviewers rule has no reviewers")
		} else {
			r.add(g, "Required reviewers", statusPass, fmt.Sprintf("%d reviewer(s)", len(revs)))
		}
		if b, _ := xm["prevent_self_review"].(bool); b {
			r.add(g, "Prevent self-review off", statusFail, "prevent_self_review is on; the person who started the run cannot approve it")
		} else {
			r.add(g, "Prevent self-review off", statusPass, "prevent_self_review is off")
		}
		return
	}
	r.add(g, "Required reviewers", statusFail, "no required_reviewers protection rule")
}

func addPresence(r *setupReport, group, name string, rules map[string][]map[string]any, typ string) {
	if len(rules[typ]) > 0 {
		r.add(group, name, statusPass, typ+" rule present")
	} else {
		r.add(group, name, statusFail, "no "+typ+" rule")
	}
}

func countRules(rules map[string][]map[string]any) int {
	n := 0
	for _, l := range rules {
		n += len(l)
	}
	return n
}

// ── small JSON helpers ───────────────────────────────────────────────

func pick(m map[string]any, keys ...string) map[string]any {
	out := make(map[string]any, len(keys))
	for _, k := range keys {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}
	return out
}

func str(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	s, _ := m[k].(string)
	return s
}

func intAt(m map[string]any, k string) int {
	if m == nil {
		return 0
	}
	switch n := m[k].(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return 0
}
