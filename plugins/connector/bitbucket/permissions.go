package main

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// opNeeds maps every operation to the Bitbucket scope family it needs and
// whether it writes. Write ops are never probed (that would mutate), so they
// are judged from the token's scopes only.
var opNeeds = []struct {
	Op    string
	Scope string // family, e.g. "repository"
	Write bool
}{
	{"search_repositories", "repository", false},
	{"get_repository", "repository", false},
	{"list_commits", "repository", false},
	{"get_commit", "repository", false},
	{"get_commit_diff", "repository", false},
	{"create_branch", "repository", true},
	{"create_file_commit", "repository", true},
	{"list_pull_requests", "pullrequest", false},
	{"get_pull_request", "pullrequest", false},
	{"list_pull_request_commits", "pullrequest", false},
	{"create_pull_request", "pullrequest", true},
	{"create_pull_request_comment", "pullrequest", true},
	{"approve_pull_request", "pullrequest", true},
	{"request_changes_pull_request", "pullrequest", true},
	{"merge_pull_request", "pullrequest", true},
	{"list_pipelines", "pipeline", false},
	{"get_pipeline", "pipeline", false},
	{"run_pipeline", "pipeline", true},
}

type probe struct {
	Name   string `json:"name"`
	Status int    `json:"status"`
	OK     bool   `json:"ok"`
}

type opPermission struct {
	Op     string `json:"op"`
	Mark   string `json:"mark"`
	Needs  string `json:"needs"`
	Reason string `json:"reason"`
}

type PermissionReport struct {
	Workspace string         `json:"workspace"`
	Repo      string         `json:"repo,omitempty"`
	Scopes    []string       `json:"scopes"`
	ScopeInfo string         `json:"scope_info"`
	Probes    []probe        `json:"probes"`
	Ops       []opPermission `json:"ops"`
	Summary   string         `json:"summary"`
}

func checkPermissions(c *connector.Ctx) (any, error) {
	return evaluatePermissions(c, strings.TrimSpace(c.Input("repo_slug")))
}

// HealthCheck backs the admin UI's "Check Permissions" button: ops the token
// can run get a check, ops it cannot get a cross with the reason. Unknown ops
// are left out so wick leaves their state untouched.
func HealthCheck(c *connector.Ctx) ([]connector.OpHealth, error) {
	rep, err := evaluatePermissions(c, "")
	if err != nil {
		return []connector.OpHealth{{Key: "auth", OK: false, Reason: err.Error()}}, nil
	}
	out := make([]connector.OpHealth, 0, len(rep.Ops))
	for _, o := range rep.Ops {
		switch o.Mark {
		case "✅":
			out = append(out, connector.OpHealth{Key: o.Op, OK: true})
		case "❌":
			out = append(out, connector.OpHealth{Key: o.Op, OK: false, Reason: o.Reason + " (needs " + o.Needs + ")"})
		}
	}
	return out, nil
}

func evaluatePermissions(c *connector.Ctx, repo string) (*PermissionReport, error) {
	workspace, err := workspace(c)
	if err != nil {
		return nil, err
	}

	targets := []struct {
		name, family string
		parts        []string
	}{
		{"repositories", "repository", []string{"repositories", workspace}},
	}
	if repo != "" {
		targets = append(targets,
			struct {
				name, family string
				parts        []string
			}{"pull requests", "pullrequest", []string{"repositories", workspace, repo, "pullrequests"}},
			struct {
				name, family string
				parts        []string
			}{"pipelines", "pipeline", []string{"repositories", workspace, repo, "pipelines"}},
		)
	}

	rep := &PermissionReport{Workspace: workspace, Repo: repo, Scopes: []string{}}
	probed := map[string]*bool{}
	var scopeHeader string
	for _, t := range targets {
		u, err := resourceURL(c, t.parts...)
		if err != nil {
			return nil, err
		}
		if t.family == "pipeline" {
			u += "/"
		}
		q := url.Values{"pagelen": {"1"}}
		req, err := http.NewRequestWithContext(c.Context(), http.MethodGet, withQuery(u, q), nil)
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		applyAuth(c, req)
		req.Header.Set("Accept", "application/json")
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return nil, fmt.Errorf("call bitbucket: %w", err)
		}
		resp.Body.Close()
		ok := resp.StatusCode >= 200 && resp.StatusCode < 300
		rep.Probes = append(rep.Probes, probe{Name: "read " + t.name, Status: resp.StatusCode, OK: ok})
		b := ok
		probed[t.family] = &b
		if h := resp.Header.Get("X-OAuth-Scopes"); h != "" && scopeHeader == "" {
			scopeHeader = h
		}
	}

	granted := map[string]bool{}
	for _, s := range strings.Split(scopeHeader, ",") {
		if s = strings.TrimSpace(s); s != "" {
			granted[s] = true
			rep.Scopes = append(rep.Scopes, s)
		}
	}
	sort.Strings(rep.Scopes)
	hasScopes := len(granted) > 0
	if hasScopes {
		rep.ScopeInfo = "scopes read from the X-OAuth-Scopes response header"
	} else {
		rep.ScopeInfo = "Bitbucket returned no scope header; read ops come from live probes, write ops cannot be verified without performing them"
	}

	allowed, denied, unknown := 0, 0, 0
	for _, n := range opNeeds {
		need := "read:" + n.Scope + ":bitbucket"
		if n.Write {
			need = "write:" + n.Scope + ":bitbucket"
		}
		p := opPermission{Op: n.Op, Needs: need}
		switch {
		case hasScopes:
			if scopeCovers(granted, n.Scope, n.Write) {
				p.Mark, p.Reason = "✅", "scope granted"
			} else {
				p.Mark, p.Reason = "❌", "scope missing on token"
			}
		case !n.Write && probed[n.Scope] != nil:
			if *probed[n.Scope] {
				p.Mark, p.Reason = "✅", "live read probe succeeded"
			} else {
				p.Mark, p.Reason = "❌", "live read probe was rejected"
			}
		case !n.Write:
			p.Mark, p.Reason = "❔", "not probed (pass repo_slug to probe this)"
		default:
			p.Mark, p.Reason = "❔", "write op, not verifiable without performing it"
		}
		switch p.Mark {
		case "✅":
			allowed++
		case "❌":
			denied++
		default:
			unknown++
		}
		rep.Ops = append(rep.Ops, p)
	}
	rep.Summary = fmt.Sprintf("%d allowed, %d denied, %d unknown", allowed, denied, unknown)
	return rep, nil
}

// scopeCovers reports whether granted holds the needed scope. In Bitbucket,
// write implies read and admin implies write.
func scopeCovers(granted map[string]bool, family string, write bool) bool {
	s := func(level string) bool { return granted[level+":"+family+":bitbucket"] }
	if write {
		return s("write") || s("admin")
	}
	return s("read") || s("write") || s("admin")
}
