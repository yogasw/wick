package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yogasw/wick/pkg/connector"
)

// routeSrv answers each "METHOD path" (query string ignored) with the given
// body; anything else is a 404 with GitHub's message. Seen records paths.
func routeSrv(t *testing.T, routes map[string]any) (*httptest.Server, *[]string) {
	t.Helper()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		body, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "Not Found"})
			return
		}
		if st, isStatus := body.(int); isStatus {
			w.WriteHeader(st)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "Resource not accessible by personal access token"})
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func settingsCtx(srv *httptest.Server, input map[string]string) *connector.Ctx {
	return newCtx(map[string]string{"base_url": srv.URL, "token": "ghp_test"}, input)
}

func TestListRulesetsTrims(t *testing.T) {
	srv, seen := routeSrv(t, map[string]any{
		"GET /repos/o/r/rulesets": []any{map[string]any{"id": 1, "name": "master", "target": "branch", "enforcement": "active", "_links": map[string]any{"self": "x"}, "node_id": "N"}},
	})
	res, err := listRulesets(settingsCtx(srv, map[string]string{"owner": "o", "repo": "r"}))
	require.NoError(t, err)
	arr := res.([]any)
	require.Len(t, arr, 1)
	m := arr[0].(map[string]any)
	assert.Equal(t, "master", m["name"])
	assert.NotContains(t, m, "_links")
	assert.NotContains(t, m, "node_id")
	assert.Equal(t, []string{"GET /repos/o/r/rulesets"}, *seen)
}

func TestSettingsOpsExplainForbidden(t *testing.T) {
	srv, _ := routeSrv(t, map[string]any{
		"GET /repos/o/r/rulesets":     403,
		"GET /repos/o/r/environments": 403,
	})
	in := map[string]string{"owner": "o", "repo": "r", "id": "7", "branch": "master", "name": "release-approval"}
	for name, fn := range map[string]func(*connector.Ctx) (any, error){
		"list_rulesets":     listRulesets,
		"get_ruleset":       getRuleset,
		"get_branch_rules":  getBranchRules,
		"list_environments": listEnvironments,
		"get_environment":   getEnvironment,
	} {
		_, err := fn(settingsCtx(srv, in))
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), "read access to the repository's settings", name)
		assert.Contains(t, err.Error(), "Administration: Read", name)
	}
}

func TestGetRuleset(t *testing.T) {
	srv, _ := routeSrv(t, map[string]any{
		"GET /repos/o/r/rulesets/7": map[string]any{"id": 7, "name": "master", "rules": []any{map[string]any{"type": "deletion"}}, "bypass_actors": []any{}, "_links": map[string]any{}},
	})
	res, err := getRuleset(settingsCtx(srv, map[string]string{"owner": "o", "repo": "r", "id": "7"}))
	require.NoError(t, err)
	m := res.(map[string]any)
	assert.Equal(t, "master", m["name"])
	assert.Contains(t, m, "rules")
	assert.NotContains(t, m, "_links")

	_, err = getRuleset(settingsCtx(srv, map[string]string{"owner": "o", "repo": "r"}))
	assert.ErrorContains(t, err, "id is required")
}

func TestGetBranchRules(t *testing.T) {
	srv, seen := routeSrv(t, map[string]any{
		"GET /repos/o/r/rules/branches/release/x": []any{map[string]any{"type": "deletion", "ruleset_id": 3}},
	})
	res, err := getBranchRules(settingsCtx(srv, map[string]string{"owner": "o", "repo": "r", "branch": "release/x"}))
	require.NoError(t, err)
	assert.Len(t, res.([]any), 1)
	// The slash is escaped on the wire; the server sees the decoded path.
	assert.Equal(t, "GET /repos/o/r/rules/branches/release/x", (*seen)[0])
}

func TestListAndGetEnvironment(t *testing.T) {
	env := map[string]any{
		"id": 1, "name": "release-approval", "node_id": "N", "url": "u",
		"protection_rules": []any{map[string]any{
			"id": 9, "type": "required_reviewers", "prevent_self_review": false,
			"reviewers": []any{map[string]any{"type": "User", "reviewer": map[string]any{"login": "yoga", "avatar_url": "a"}}},
		}},
	}
	srv, _ := routeSrv(t, map[string]any{
		"GET /repos/o/r/environments":                  map[string]any{"total_count": 1, "environments": []any{env}},
		"GET /repos/o/r/environments/release-approval": env,
	})
	res, err := listEnvironments(settingsCtx(srv, map[string]string{"owner": "o", "repo": "r"}))
	require.NoError(t, err)
	envs := res.(map[string]any)["environments"].([]any)
	require.Len(t, envs, 1)
	e := envs[0].(map[string]any)
	assert.NotContains(t, e, "node_id")
	rule := e["protection_rules"].([]any)[0].(map[string]any)
	assert.Equal(t, []any{map[string]any{"type": "User", "name": "yoga"}}, rule["reviewers"])

	one, err := getEnvironment(settingsCtx(srv, map[string]string{"owner": "o", "repo": "r", "name": "release-approval"}))
	require.NoError(t, err)
	assert.Equal(t, "release-approval", one.(map[string]any)["name"])
}

// goodSetup is a repository configured exactly as agreed.
func goodSetup() map[string]any {
	adminBypass := []any{map[string]any{"actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always"}}
	return map[string]any{
		"GET /repos/o/r/rulesets": []any{
			map[string]any{"id": 1, "name": "master", "target": "branch", "enforcement": "active"},
			map[string]any{"id": 2, "name": "develop", "target": "branch", "enforcement": "active"},
			map[string]any{"id": 3, "name": "tags", "target": "tag", "enforcement": "active"},
		},
		"GET /repos/o/r/rulesets/1": map[string]any{"id": 1, "name": "master", "bypass_actors": adminBypass},
		"GET /repos/o/r/rulesets/2": map[string]any{"id": 2, "name": "develop", "bypass_actors": adminBypass},
		"GET /repos/o/r/rulesets/3": map[string]any{
			"id": 3, "name": "tags", "target": "tag",
			"conditions": map[string]any{"ref_name": map[string]any{"include": []any{"refs/tags/v*", "refs/tags/*/v*"}, "exclude": []any{}}},
			"rules":      []any{map[string]any{"type": "update"}, map[string]any{"type": "deletion"}},
		},
		"GET /repos/o/r/rules/branches/master": []any{
			map[string]any{"type": "pull_request", "ruleset_id": 1, "parameters": map[string]any{"required_approving_review_count": 0, "allowed_merge_methods": []any{"squash"}}},
			map[string]any{"type": "required_status_checks", "ruleset_id": 1, "parameters": map[string]any{"required_status_checks": []any{map[string]any{"context": "master-source-guard"}}}},
			map[string]any{"type": "non_fast_forward", "ruleset_id": 1},
			map[string]any{"type": "deletion", "ruleset_id": 1},
		},
		"GET /repos/o/r/rules/branches/release": []any{},
		"GET /repos/o/r/rules/branches/develop": []any{
			map[string]any{"type": "pull_request", "ruleset_id": 2, "parameters": map[string]any{"required_approving_review_count": 0, "allowed_merge_methods": []any{"merge", "rebase"}}},
			map[string]any{"type": "non_fast_forward", "ruleset_id": 2},
			map[string]any{"type": "deletion", "ruleset_id": 2},
		},
		"GET /repos/o/r/environments": map[string]any{"total_count": 1, "environments": []any{}},
		"GET /repos/o/r/environments/release-approval": map[string]any{
			"name": "release-approval",
			"protection_rules": []any{map[string]any{
				"type": "required_reviewers", "prevent_self_review": false,
				"reviewers": []any{map[string]any{"type": "User", "reviewer": map[string]any{"login": "yoga"}}},
			}},
		},
	}
}

func statusByName(r *setupReport) map[string]string {
	out := map[string]string{}
	for _, c := range r.Checks {
		out[c.Group+"/"+c.Name] = c.Status
	}
	return out
}

func TestValidateRepoSetupPass(t *testing.T) {
	srv, _ := routeSrv(t, goodSetup())
	// owner/repo come from the instance defaults.
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "ghp_test", "default_owner": "o", "default_repo": "r"}, map[string]string{})
	res, err := validateRepoSetup(c)
	require.NoError(t, err)
	r := res.(*setupReport)
	for _, ch := range r.Checks {
		assert.Equal(t, statusPass, ch.Status, "%s/%s: %s", ch.Group, ch.Name, ch.Reason)
	}
	assert.Zero(t, r.Failed)
	assert.Zero(t, r.Warned)
	st := statusByName(r)
	assert.Len(t, r.Checks, 23)
	for _, k := range []string{
		"Token/Can read rulesets", "Token/Can read environments", "Token/Can read bypass lists",
		"master/Ruleset active", "master/Pull request required", "master/Approvals 0 or admin bypass",
		"master/Squash merge only", "master/Required check master-source-guard",
		"master/Force push blocked", "master/Deletion restricted", "master/Not applied to release",
		"release/Merge commit allowed",
		"develop/Pull request required", "develop/Linear history off", "develop/Squash not allowed (recommended)",
		"develop/Force push blocked", "develop/Deletion restricted", "develop/Admin bypass",
		"tags/Tags v* protected", "tags/Tags */v* protected",
		"environment/release-approval exists", "environment/Required reviewers", "environment/Prevent self-review off",
	} {
		assert.Contains(t, st, k)
	}
}

func TestValidateRepoSetupFail(t *testing.T) {
	routes := goodSetup()
	// master: approvals 2 with no admin bypass, all merge methods, no force-push block.
	routes["GET /repos/o/r/rulesets/1"] = map[string]any{"id": 1, "name": "master", "bypass_actors": []any{}}
	routes["GET /repos/o/r/rules/branches/master"] = []any{
		map[string]any{"type": "pull_request", "ruleset_id": 1, "parameters": map[string]any{"required_approving_review_count": 2}},
		map[string]any{"type": "deletion", "ruleset_id": 1},
	}
	// release caught by a squash-only rule.
	routes["GET /repos/o/r/rules/branches/release"] = []any{
		map[string]any{"type": "pull_request", "ruleset_id": 1, "parameters": map[string]any{"allowed_merge_methods": []any{"squash"}}},
	}
	// develop: linear history on, squash allowed, bypass list hidden from the token.
	routes["GET /repos/o/r/rulesets/2"] = map[string]any{"id": 2, "name": "develop"}
	routes["GET /repos/o/r/rules/branches/develop"] = []any{
		map[string]any{"type": "pull_request", "ruleset_id": 2, "parameters": map[string]any{"allowed_merge_methods": []any{"merge", "squash"}}},
		map[string]any{"type": "required_linear_history", "ruleset_id": 2},
	}
	// tags: only v* covered.
	routes["GET /repos/o/r/rulesets/3"] = map[string]any{
		"id": 3, "name": "tags",
		"conditions": map[string]any{"ref_name": map[string]any{"include": []any{"refs/tags/v*"}}},
		"rules":      []any{map[string]any{"type": "update"}, map[string]any{"type": "deletion"}},
	}
	// environment: self-review prevented.
	routes["GET /repos/o/r/environments/release-approval"] = map[string]any{
		"name":             "release-approval",
		"protection_rules": []any{map[string]any{"type": "required_reviewers", "prevent_self_review": true, "reviewers": []any{}}},
	}
	srv, _ := routeSrv(t, routes)
	res, err := validateRepoSetup(settingsCtx(srv, map[string]string{"owner": "o", "repo": "r"}))
	require.NoError(t, err)
	st := statusByName(res.(*setupReport))

	for k, want := range map[string]string{
		"master/Approvals 0 or admin bypass":        statusFail,
		"master/Squash merge only":                  statusFail,
		"master/Force push blocked":                 statusFail,
		"master/Deletion restricted":                statusPass,
		"master/Not applied to release":             statusFail,
		"master/Required check master-source-guard": statusWarn,
		"release/Merge commit allowed":              statusFail,
		"develop/Deletion restricted":               statusFail,
		"Token/Can read bypass lists":               statusPass,
		"develop/Linear history off":                statusFail,
		"develop/Squash not allowed (recommended)":  statusWarn,
		"develop/Force push blocked":                statusFail,
		"develop/Admin bypass":                      statusFail,
		"tags/Tags v* protected":                    statusPass,
		"tags/Tags */v* protected":                  statusFail,
		"environment/Required reviewers":            statusFail,
		"environment/Prevent self-review off":       statusFail,
		"Token/Can read rulesets":                   statusPass,
	} {
		assert.Equal(t, want, st[k], k)
	}
}

func TestValidateRepoSetupNoAdminRead(t *testing.T) {
	srv, _ := routeSrv(t, map[string]any{
		"GET /repos/o/r/rulesets":     403,
		"GET /repos/o/r/environments": 403,
	})
	res, err := validateRepoSetup(settingsCtx(srv, map[string]string{"owner": "o", "repo": "r"}))
	require.NoError(t, err, "an unreadable endpoint is a failed check, never a failed op")
	r := res.(*setupReport)
	st := statusByName(r)
	assert.Equal(t, statusFail, st["Token/Can read rulesets"])
	assert.Equal(t, statusFail, st["Token/Can read environments"])
	assert.Equal(t, statusFail, st["master/Ruleset active"])
	assert.Equal(t, statusFail, st["environment/release-approval exists"])
	assert.Zero(t, r.Passed)
	for _, ch := range r.Checks {
		if ch.Group == "Token" && ch.Name != "Can read bypass lists" {
			assert.Contains(t, ch.Reason, "read access to the repository's settings")
		}
	}
}

func TestValidateRepoSetupNeedsRepo(t *testing.T) {
	_, err := validateRepoSetup(newCtx(map[string]string{"token": "ghp_test"}, map[string]string{}))
	assert.ErrorContains(t, err, "owner and repo are required")
}

func TestRefMatches(t *testing.T) {
	rs := map[string]any{"conditions": map[string]any{"ref_name": map[string]any{
		"include": []any{"refs/tags/*/v*"}, "exclude": []any{"refs/tags/skip/v*"},
	}}}
	assert.True(t, refMatches(rs, "refs/tags/github/v0.2.2"))
	assert.False(t, refMatches(rs, "refs/tags/v1.0.0"))
	assert.False(t, refMatches(rs, "refs/tags/skip/v1"))
	all := map[string]any{"conditions": map[string]any{"ref_name": map[string]any{"include": []any{"~ALL"}}}}
	assert.True(t, refMatches(all, "refs/tags/v1"))
	deep := map[string]any{"conditions": map[string]any{"ref_name": map[string]any{"include": []any{"refs/tags/**"}}}}
	assert.True(t, refMatches(deep, "refs/tags/github/v0.2.2"))
	assert.True(t, refMatches(deep, "refs/tags/v1"))
	assert.False(t, refMatches(deep, "refs/heads/master"))
}

func TestTokenAccessPanel(t *testing.T) {
	const secret = "ghp_SECRETvalue123"
	routes := goodSetup()
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/user" {
			w.Header().Set("X-OAuth-Scopes", "repo, read:org")
			w.Header().Set("GitHub-Authentication-Token-Expiration", "2027-01-01 00:00:00 UTC")
			_ = json.NewEncoder(w).Encode(map[string]any{"login": "octo", "name": "Octo Cat", "type": "User"})
			return
		}
		body, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "Not Found"})
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)

	cfg := map[string]string{"base_url": srv.URL, "token": secret, "default_owner": "o", "default_repo": "r"}
	// Opening the page: GET /user only, no validation.
	res, err := tokenAccess(newCtx(cfg, map[string]string{"browser": ""}))
	require.NoError(t, err)
	h := res.(map[string]any)["html"].(string)
	assert.Equal(t, []string{"/user"}, seen, "opening the panel must cost one request")
	assert.NotContains(t, h, "passed,")
	assert.Contains(t, h, `data-arg="validate"`)

	// Pressing Validate setup.
	res, err = tokenAccess(newCtx(cfg, map[string]string{"browser": "validate"}))
	require.NoError(t, err)
	h = res.(map[string]any)["html"].(string)

	assert.NotContains(t, h, secret, "token value must never be rendered")
	assert.NotContains(t, h, "SECRETvalue")
	for _, want := range []string{
		"Octo Cat", "octo", "classic personal access token", "ghp_…",
		"read:org, repo", "2027-01-01", "o/r",
		`data-op="token_access"`, "Validate setup", `name="owner"`, `name="repo"`,
		"Repository Settings", "Tags */v* protected", "23 passed, 0 failed, 0 warnings",
	} {
		assert.Contains(t, h, want)
	}
	assert.NotContains(t, h, "missing scope", "repo scope covers every category")
	assert.NotContains(t, h, "token_access</td>", "the config-only op is not listed as a category op")
	assert.NotContains(t, h, `class="bg-`, "no Tailwind classes in runtime markup")
}

func TestTokenAccessPanelErrors(t *testing.T) {
	// No token: banner, no validation.
	res, err := tokenAccess(newCtx(map[string]string{}, map[string]string{"owner": "o", "repo": "r"}))
	require.NoError(t, err)
	h := res.(map[string]any)["html"].(string)
	assert.Contains(t, h, "No token is stored")
	assert.Contains(t, h, "Fix the token first")

	// Bad token: GET /user 401, the error is shown, the token is not.
	const secret = "github_pat_BADvalue"
	srv := mockSrv(t, 401, map[string]any{"message": "Bad credentials"})
	res, err = tokenAccess(newCtx(map[string]string{"base_url": srv.URL, "token": secret}, map[string]string{}))
	require.NoError(t, err)
	h = res.(map[string]any)["html"].(string)
	assert.Contains(t, h, "Bad credentials")
	assert.Contains(t, h, "fine-grained personal access token")
	assert.NotContains(t, h, "BADvalue")
	assert.True(t, strings.Count(h, `data-op=`) == 2, "Recheck plus Validate setup")
}

func TestTokenAccessIsConfigOnly(t *testing.T) {
	var found bool
	for _, cat := range Operations() {
		for _, op := range cat.Ops {
			switch op.Key {
			case "token_access":
				found = true
				assert.True(t, op.ConfigOnly)
			case "list_rulesets", "get_ruleset", "get_branch_rules", "list_environments", "get_environment", "validate_repo_setup":
				assert.False(t, op.Destructive, op.Key)
				assert.False(t, op.ConfigOnly, op.Key)
			}
		}
	}
	assert.True(t, found)
}

func validateWith(t *testing.T, routes map[string]any) map[string]string {
	t.Helper()
	srv, _ := routeSrv(t, routes)
	res, err := validateRepoSetup(settingsCtx(srv, map[string]string{"owner": "o", "repo": "r"}))
	require.NoError(t, err)
	return statusByName(res.(*setupReport))
}

// Bypass is per ruleset: an admin bypass on a second ruleset does not lift
// the approvals asked by the first.
func TestValidateMultiRulesetBypass(t *testing.T) {
	routes := goodSetup()
	routes["GET /repos/o/r/rulesets/1"] = map[string]any{"id": 1, "name": "master", "bypass_actors": []any{}}
	routes["GET /repos/o/r/rulesets/9"] = map[string]any{"id": 9, "name": "repo-wide", "bypass_actors": []any{map[string]any{"actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "always"}}}
	routes["GET /repos/o/r/rules/branches/master"] = []any{
		map[string]any{"type": "pull_request", "ruleset_id": 1, "parameters": map[string]any{"required_approving_review_count": 1, "allowed_merge_methods": []any{"squash"}}},
		map[string]any{"type": "deletion", "ruleset_id": 9},
	}
	assert.Equal(t, statusFail, validateWith(t, routes)["master/Approvals 0 or admin bypass"])

	// The same approvals with admin bypass on the ruleset that asks for them pass.
	routes["GET /repos/o/r/rulesets/1"] = map[string]any{"id": 1, "name": "master", "bypass_actors": []any{map[string]any{"actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": "pull_request"}}}
	assert.Equal(t, statusPass, validateWith(t, routes)["master/Approvals 0 or admin bypass"])

	// develop: every ruleset blocking a direct push needs the bypass.
	routes = goodSetup()
	routes["GET /repos/o/r/rulesets/8"] = map[string]any{"id": 8, "name": "checks", "bypass_actors": []any{}}
	routes["GET /repos/o/r/rules/branches/develop"] = append(routes["GET /repos/o/r/rules/branches/develop"].([]any),
		map[string]any{"type": "required_status_checks", "ruleset_id": 8, "parameters": map[string]any{}})
	assert.Equal(t, statusFail, validateWith(t, routes)["develop/Admin bypass"])
}

func TestValidateDevelopBypassMode(t *testing.T) {
	for mode, want := range map[string]string{"always": statusPass, "exempt": statusPass, "pull_request": statusFail} {
		routes := goodSetup()
		routes["GET /repos/o/r/rulesets/2"] = map[string]any{"id": 2, "name": "develop", "bypass_actors": []any{map[string]any{"actor_id": 5, "actor_type": "RepositoryRole", "bypass_mode": mode}}}
		assert.Equal(t, want, validateWith(t, routes)["develop/Admin bypass"], mode)
	}
}

// Several pull_request rules: highest approvals, intersection of methods.
func TestValidateMergesPullRequestRules(t *testing.T) {
	routes := goodSetup()
	routes["GET /repos/o/r/rulesets/1"] = map[string]any{"id": 1, "name": "master", "bypass_actors": []any{}}
	routes["GET /repos/o/r/rulesets/9"] = map[string]any{"id": 9, "name": "org", "bypass_actors": []any{}}
	routes["GET /repos/o/r/rules/branches/master"] = []any{
		map[string]any{"type": "pull_request", "ruleset_id": 1, "parameters": map[string]any{"required_approving_review_count": 0, "allowed_merge_methods": []any{"squash"}}},
		map[string]any{"type": "pull_request", "ruleset_id": 9, "parameters": map[string]any{"required_approving_review_count": 1, "allowed_merge_methods": []any{"merge", "squash", "rebase"}}},
	}
	st := validateWith(t, routes)
	assert.Equal(t, statusFail, st["master/Approvals 0 or admin bypass"], "the second rule's approval counts")
	assert.Equal(t, statusPass, st["master/Squash merge only"], "squash is the only common method")

	routes["GET /repos/o/r/rules/branches/master"] = []any{
		map[string]any{"type": "pull_request", "ruleset_id": 1, "parameters": map[string]any{"allowed_merge_methods": []any{"squash"}}},
		map[string]any{"type": "pull_request", "ruleset_id": 9, "parameters": map[string]any{"allowed_merge_methods": []any{"merge"}}},
	}
	assert.Equal(t, statusFail, validateWith(t, routes)["master/Squash merge only"], "no common method")
}

func TestValidateRelease(t *testing.T) {
	// A separate release ruleset that allows merge commits is fine.
	routes := goodSetup()
	routes["GET /repos/o/r/rulesets/4"] = map[string]any{"id": 4, "name": "release", "bypass_actors": []any{}}
	routes["GET /repos/o/r/rules/branches/release"] = []any{
		map[string]any{"type": "deletion", "ruleset_id": 4},
		map[string]any{"type": "pull_request", "ruleset_id": 4, "parameters": map[string]any{"allowed_merge_methods": []any{"merge"}}},
	}
	st := validateWith(t, routes)
	assert.Equal(t, statusPass, st["master/Not applied to release"])
	assert.Equal(t, statusPass, st["release/Merge commit allowed"])

	// The master ruleset reaching release fails, even when it adds only a
	// status check.
	routes["GET /repos/o/r/rules/branches/release"] = []any{
		map[string]any{"type": "required_status_checks", "ruleset_id": 1, "parameters": map[string]any{}},
	}
	st = validateWith(t, routes)
	assert.Equal(t, statusFail, st["master/Not applied to release"])
	assert.Equal(t, statusPass, st["release/Merge commit allowed"])

	// Linear history on release refuses release.yml's merge commit.
	routes["GET /repos/o/r/rules/branches/release"] = []any{map[string]any{"type": "required_linear_history", "ruleset_id": 4}}
	st = validateWith(t, routes)
	assert.Equal(t, statusPass, st["master/Not applied to release"])
	assert.Equal(t, statusFail, st["release/Merge commit allowed"])
}

func TestValidateBypassListProbe(t *testing.T) {
	routes := goodSetup()
	routes["GET /repos/o/r/rulesets/1"] = map[string]any{"id": 1, "name": "master"}
	assert.Equal(t, statusFail, validateWith(t, routes)["Token/Can read bypass lists"])

	routes = goodSetup()
	routes["GET /repos/o/r/rulesets"] = []any{}
	assert.Equal(t, statusWarn, validateWith(t, routes)["Token/Can read bypass lists"])
}

func TestValidateEnvironmentMissing(t *testing.T) {
	routes := goodSetup()
	delete(routes, "GET /repos/o/r/environments/release-approval")
	srv, _ := routeSrv(t, routes)
	res, err := validateRepoSetup(settingsCtx(srv, map[string]string{"owner": "o", "repo": "r"}))
	require.NoError(t, err)
	for _, ch := range res.(*setupReport).Checks {
		if ch.Name == "release-approval exists" {
			assert.Equal(t, statusFail, ch.Status)
			assert.Equal(t, "environment release-approval does not exist", ch.Reason)
		}
	}
}

func TestSettingsRejectBadNames(t *testing.T) {
	srv, seen := routeSrv(t, map[string]any{})
	for _, in := range []map[string]string{
		{"owner": "o", "repo": "r/../../user"},
		{"owner": "..", "repo": "r"},
		{"owner": "o", "repo": "r?x=1"},
	} {
		_, err := validateRepoSetup(settingsCtx(srv, in))
		assert.ErrorContains(t, err, "invalid owner/repo")
		_, err = listRulesets(settingsCtx(srv, in))
		assert.ErrorContains(t, err, "invalid owner/repo")
	}
	assert.Empty(t, *seen, "nothing is requested for a bad name")
}

func TestListRulesetsIncludesParents(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		_ = json.NewEncoder(w).Encode([]any{})
	}))
	t.Cleanup(srv.Close)
	for _, v := range []string{"", "false", "true"} {
		_, err := listRulesets(settingsCtx(srv, map[string]string{"owner": "o", "repo": "r", "includes_parents": v}))
		require.NoError(t, err)
	}
	assert.Equal(t, []string{"per_page=100", "per_page=100&includes_parents=false", "per_page=100&includes_parents=true"}, queries)
}

func TestTokenAccessValidateTimeout(t *testing.T) {
	old := panelValidateTimeout
	panelValidateTimeout = 50 * time.Millisecond
	t.Cleanup(func() { panelValidateTimeout = old })
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			_ = json.NewEncoder(w).Encode(map[string]any{"login": "octo"})
			return
		}
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	t.Cleanup(srv.Close)
	start := time.Now()
	res, err := tokenAccess(newCtx(map[string]string{"base_url": srv.URL, "token": "ghp_x"}, map[string]string{"browser": "validate", "owner": "o", "repo": "r"}))
	require.NoError(t, err)
	assert.Less(t, time.Since(start), 2*time.Second, "the deadline covers the whole run")
	h := res.(map[string]any)["html"].(string)
	assert.Contains(t, h, "Finished in time")
	assert.Contains(t, h, "timed out after 50ms")
}

func TestTokenAccessEscapes(t *testing.T) {
	routes := goodSetup()
	routes["GET /repos/o/r/rules/branches/release"] = []any{map[string]any{"type": "deletion", "ruleset_id": 1}, map[string]any{"type": "pull_request", "ruleset_id": 1}}
	routes["GET /repos/o/r/rulesets/1"] = map[string]any{"id": 1, "name": "<script>x</script>", "bypass_actors": []any{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/user" {
			_ = json.NewEncoder(w).Encode(map[string]any{"login": "<img src=x>"})
			return
		}
		body, ok := routes[r.Method+" "+r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "Not Found"})
			return
		}
		_ = json.NewEncoder(w).Encode(body)
	}))
	t.Cleanup(srv.Close)
	res, err := tokenAccess(newCtx(map[string]string{"base_url": srv.URL, "token": "ghp_x"}, map[string]string{"browser": "validate", "owner": "o", "repo": "r"}))
	require.NoError(t, err)
	h := res.(map[string]any)["html"].(string)
	assert.NotContains(t, h, "<script>x")
	assert.NotContains(t, h, "<img src=x>")
	assert.Contains(t, h, "&lt;script&gt;")
}
