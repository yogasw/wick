package main

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/yogasw/wick/pkg/connector"
)

// GitLab access levels. A project member's role decides what the token's
// user may do inside that project, on top of the token's scopes.
const (
	roleNone       = 0
	roleGuest      = 10
	roleReporter   = 20
	roleDeveloper  = 30
	roleMaintainer = 40
	roleOwner      = 50
)

var roleNames = map[int]string{
	roleNone: "none", 5: "Minimal access", roleGuest: "Guest", 15: "Planner",
	roleReporter: "Reporter", roleDeveloper: "Developer", roleMaintainer: "Maintainer", roleOwner: "Owner",
}

func roleName(level int) string {
	if n, ok := roleNames[level]; ok {
		return n
	}
	return fmt.Sprintf("level %d", level)
}

// opNeeds maps every operation to whether it writes (scope api, else
// read_api is enough) and the lowest project role it needs. Role 0 = the op
// is not project-scoped. Read ops on a public/internal project need no role.
var opNeeds = []struct {
	Op    string
	Write bool
	Role  int
}{
	{"get_me", false, roleNone},
	{"list_projects", false, roleNone},
	{"get_project", false, roleNone},
	{"list_branches", false, roleReporter},
	{"create_branch", true, roleDeveloper},
	{"list_commits", false, roleReporter},
	{"get_commit", false, roleReporter},
	{"compare", false, roleReporter},
	{"get_file", false, roleReporter},
	{"list_tree", false, roleReporter},
	{"list_tags", false, roleReporter},
	{"list_merge_requests", false, roleReporter},
	{"get_merge_request", false, roleReporter},
	{"get_merge_request_changes", false, roleReporter},
	{"list_mr_notes", false, roleReporter},
	{"list_mr_pipelines", false, roleReporter},
	{"create_merge_request", true, roleDeveloper},
	{"update_merge_request", true, roleDeveloper},
	{"add_mr_note", true, roleReporter},
	{"approve_merge_request", true, roleDeveloper},
	{"merge_merge_request", true, roleDeveloper},
	{"list_pipelines", false, roleReporter},
	{"get_pipeline", false, roleReporter},
	{"list_pipeline_jobs", false, roleReporter},
	{"get_job_log", false, roleReporter},
	{"retry_pipeline", true, roleDeveloper},
	{"cancel_pipeline", true, roleDeveloper},
	{"list_issues", false, roleGuest},
	{"get_issue", false, roleGuest},
	{"create_issue", true, roleGuest},
	{"add_issue_note", true, roleGuest},
}

type opPermission struct {
	Op     string `json:"op"`
	Mark   string `json:"mark"`
	Needs  string `json:"needs"`
	Reason string `json:"reason"`
}

// PermissionReport is what the config page's checklist is drawn from.
type PermissionReport struct {
	User       string         `json:"user"`
	TokenName  string         `json:"token_name,omitempty"`
	Scopes     []string       `json:"scopes"`
	ScopeInfo  string         `json:"scope_info"`
	ExpiresAt  string         `json:"expires_at,omitempty"`
	Project    string         `json:"project,omitempty"`
	Visibility string         `json:"visibility,omitempty"`
	Role       string         `json:"role,omitempty"`
	RoleInfo   string         `json:"role_info"`
	Ops        []opPermission `json:"ops"`
	Summary    string         `json:"summary"`
}

func evaluatePermissions(c *connector.Ctx, project string) (*PermissionReport, error) {
	me, err := doJSON(c, http.MethodGet, apiURL(c, "/user", nil), nil)
	if err != nil {
		return nil, err
	}
	user, _ := me.(map[string]any)
	rep := &PermissionReport{Scopes: []string{}, User: str(user["username"])}
	isAdmin, _ := user["is_admin"].(bool)

	// The token describes itself (GitLab 15.5+); older hosts answer 404 and
	// the scopes stay unknown.
	granted := map[string]bool{}
	if v, err := doJSON(c, http.MethodGet, apiURL(c, "/personal_access_tokens/self", nil), nil); err == nil {
		tok, _ := v.(map[string]any)
		rep.TokenName = str(tok["name"])
		rep.ExpiresAt = str(tok["expires_at"])
		if list, ok := tok["scopes"].([]any); ok {
			for _, s := range list {
				if s := str(s); s != "" {
					granted[s] = true
					rep.Scopes = append(rep.Scopes, s)
				}
			}
		}
		sort.Strings(rep.Scopes)
		rep.ScopeInfo = "scopes read from /personal_access_tokens/self"
	} else {
		rep.ScopeInfo = "this GitLab does not describe the token (" + err.Error() + "); read ops pass because /user answered, write ops cannot be verified without performing them"
	}
	hasScopes := len(rep.Scopes) > 0

	// Role in the project: members/all includes the ones inherited from
	// parent groups, which permissions.project_access leaves out.
	role, roleKnown, public := roleNone, false, false
	if project != "" {
		rep.Project = project
		pid := "/projects/" + url.PathEscape(project)
		p, err := doJSON(c, http.MethodGet, apiURL(c, pid, nil), nil)
		if err != nil {
			rep.RoleInfo = "project cannot be read: " + err.Error()
			roleKnown = true
		} else {
			pm, _ := p.(map[string]any)
			rep.Visibility = str(pm["visibility"])
			public = rep.Visibility == "public" || rep.Visibility == "internal"
			switch {
			case isAdmin:
				role, roleKnown = roleOwner, true
				rep.RoleInfo = "the token's user is an instance admin"
			default:
				id := fmt.Sprint(user["id"])
				m, err := doJSON(c, http.MethodGet, apiURL(c, pid+"/members/all/"+url.PathEscape(id), nil), nil)
				var ae *apiError
				switch {
				case err == nil:
					mm, _ := m.(map[string]any)
					if f, ok := mm["access_level"].(float64); ok {
						role = int(f)
					}
					roleKnown = true
					rep.RoleInfo = "role read from the project's members (inherited included)"
				case errors.As(err, &ae) && ae.Status == http.StatusNotFound:
					roleKnown = true
					rep.RoleInfo = "the token's user is not a member of this project"
				default:
					rep.RoleInfo = "role cannot be read: " + err.Error()
				}
			}
		}
		rep.Role = roleName(role)
	} else {
		rep.RoleInfo = "fill default_project to check the role in a project"
	}

	allowed, denied, unknown := 0, 0, 0
	for _, n := range opNeeds {
		p := opPermission{Op: n.Op}
		scope := "read_api"
		if n.Write {
			scope = "api"
		}
		need := []string{"scope " + scope}
		roleNeeded := n.Role
		if !n.Write && public {
			roleNeeded = roleNone
		}
		if roleNeeded > roleNone {
			need = append(need, "role "+roleName(roleNeeded)+"+")
		}
		p.Needs = strings.Join(need, ", ")

		scopeOK, scopeKnown := scopeCovers(granted, n.Write) || (n.Op == "get_me" && granted["read_user"]), hasScopes
		if !hasScopes && !n.Write {
			scopeOK, scopeKnown = true, true // /user answered, so the token reads
		}
		switch {
		case scopeKnown && !scopeOK:
			p.Mark, p.Reason = "❌", "token has no "+scope+" scope"
		case roleNeeded == roleNone && scopeKnown:
			p.Mark, p.Reason = "✅", "scope granted"
		case roleNeeded == roleNone:
			p.Mark, p.Reason = "❔", "write op, scopes unknown on this GitLab"
		case project == "" || !roleKnown:
			p.Mark, p.Reason = "❔", rep.RoleInfo
		case role < roleNeeded:
			p.Mark, p.Reason = "❌", "role "+roleName(role)+" in "+project+", needs "+roleName(roleNeeded)
		case !scopeKnown:
			p.Mark, p.Reason = "❔", "role ok, write scope unknown on this GitLab"
		default:
			p.Mark, p.Reason = "✅", "scope and role ok"
		}
		if n.Op == "merge_merge_request" && p.Mark == "✅" && role < roleMaintainer {
			p.Reason += "; a protected target branch may still need Maintainer"
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

// scopeCovers: api grants everything, read_api only reads. read_user is
// only enough for get_me, which the caller handles.
func scopeCovers(granted map[string]bool, write bool) bool {
	if write {
		return granted["api"]
	}
	return granted["api"] || granted["read_api"]
}

func str(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// permissionStatus renders the checklist widget shown on the connector
// config page (same html-field mechanism as the Bitbucket connector).
func permissionStatus(c *connector.Ctx) (any, error) {
	if strings.TrimSpace(c.Cfg("token")) == "" {
		return map[string]any{"html": permCard(false, "Isi token dulu untuk mengecek permission.", nil, nil)}, nil
	}
	project := strings.Trim(strings.TrimSpace(c.Cfg("default_project")), "/")
	rep, err := evaluatePermissions(c, project)
	if err != nil {
		return map[string]any{"html": permCard(false, html.EscapeString(err.Error()), nil, nil)}, nil
	}
	allowed, denied, unknown := 0, 0, 0
	for _, o := range rep.Ops {
		switch o.Mark {
		case "✅":
			allowed++
		case "❌":
			denied++
		default:
			unknown++
		}
	}
	head := fmt.Sprintf("%d allowed · %d denied", allowed, denied)
	if unknown > 0 {
		head += fmt.Sprintf(" · %d unknown", unknown)
	}
	return map[string]any{"html": permCard(denied == 0, head, tokenFacts(rep, time.Now()), rep.Ops)}, nil
}

// tokenFacts are the lines above the checklist: who, which scopes, until
// when, and the role in default_project.
func tokenFacts(rep *PermissionReport, now time.Time) []string {
	var out []string
	who := "user <b>" + html.EscapeString(rep.User) + "</b>"
	if rep.TokenName != "" {
		who += " · token <b>" + html.EscapeString(rep.TokenName) + "</b>"
	}
	out = append(out, who)
	if len(rep.Scopes) > 0 {
		out = append(out, "scopes: <span class=\"font-mono\">"+html.EscapeString(strings.Join(rep.Scopes, ", "))+"</span>")
	} else {
		out = append(out, "scopes: unknown — "+html.EscapeString(rep.ScopeInfo))
	}
	switch exp, err := time.Parse("2006-01-02", rep.ExpiresAt); {
	case rep.ExpiresAt == "":
		if rep.TokenName != "" {
			out = append(out, "expires: never")
		}
	case err == nil:
		days := int(exp.Sub(now.Truncate(24*time.Hour)).Hours() / 24)
		line := "expires: " + html.EscapeString(rep.ExpiresAt) + fmt.Sprintf(" (%d days left)", days)
		if days <= 14 {
			line = "<span class=\"text-neg-400\">⚠ " + line + "</span>"
		}
		out = append(out, line)
	default:
		out = append(out, "expires: "+html.EscapeString(rep.ExpiresAt))
	}
	if rep.Project != "" {
		line := "project <span class=\"font-mono\">" + html.EscapeString(rep.Project) + "</span>"
		if rep.Visibility != "" {
			line += " (" + html.EscapeString(rep.Visibility) + ")"
		}
		line += ": role <b>" + html.EscapeString(rep.Role) + "</b> — " + html.EscapeString(rep.RoleInfo)
		out = append(out, line)
	} else {
		out = append(out, html.EscapeString(rep.RoleInfo))
	}
	return out
}

func permCard(ok bool, head string, facts []string, ops []opPermission) string {
	ring, text, dot := "border-neg-300 bg-neg-100 dark:bg-navy-800", "text-neg-400", "bg-neg-400"
	if ok {
		ring, text, dot = "border-pos-300 bg-pos-100 dark:bg-navy-800", "text-pos-400", "bg-pos-400"
	}
	var b strings.Builder
	b.WriteString(`<div class="rounded-lg border ` + ring + ` px-4 py-2.5">`)
	b.WriteString(`<div class="flex items-center gap-2"><span class="h-2 w-2 rounded-full ` + dot + `"></span>`)
	b.WriteString(`<span class="text-xs font-medium ` + text + `">` + head + `</span></div>`)
	if len(facts) > 0 {
		b.WriteString(`<ul class="mt-2 space-y-0.5">`)
		for _, f := range facts {
			b.WriteString(`<li class="text-xs opacity-80">` + f + `</li>`)
		}
		b.WriteString(`</ul>`)
	}
	if len(ops) > 0 {
		b.WriteString(`<ul class="mt-2 grid grid-cols-1 gap-x-6 gap-y-1 sm:grid-cols-2">`)
		for _, o := range ops {
			mark, cls := "❔", ""
			switch o.Mark {
			case "✅":
				mark, cls = "✅", "text-pos-400"
			case "❌":
				mark, cls = "❌", "text-neg-400"
			}
			b.WriteString(`<li class="text-xs ` + cls + `" title="` + html.EscapeString(o.Reason) + `">` + mark + ` <span class="font-mono">` + html.EscapeString(o.Op) + `</span>`)
			if o.Mark != "✅" {
				b.WriteString(` <span class="opacity-70">(` + html.EscapeString(o.Needs) + `)</span>`)
			}
			b.WriteString(`</li>`)
		}
		b.WriteString(`</ul>`)
	}
	b.WriteString(`</div>`)
	return b.String()
}
