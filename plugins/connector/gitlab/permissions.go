package main

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strconv"
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

// opNeeds maps every operation to the probe family that shows whether the
// token can read that kind of resource, whether it writes (scope api, else
// read_api is enough) and the lowest project role it needs (0 = not
// project-scoped; reads on a public/internal project need no role).
//
// Granular is the permission a fine-grained token needs for it, named as
// GitLab's token form shows it: "[User ›] Category › Resource: Action"
// (from GitLab's config/authz assignable permissions; no "User ›" prefix =
// the "Group and project" tab).
var opNeeds = []struct {
	Op       string
	Family   string
	Write    bool
	Role     int
	Granular string
}{
	{"get_me", "user", false, roleNone, "User › System Access › User: Read"},
	{"list_projects", "projects", false, roleNone, "User › Projects › Project: Read"},
	{"get_project", "project", false, roleNone, "Projects › Project: Read"},
	{"list_branches", "branches", false, roleReporter, "Repository › Branch: Read"},
	{"create_branch", "branches", true, roleDeveloper, "Repository › Branch: Create"},
	{"list_commits", "commits", false, roleReporter, "Repository › Commit: Read"},
	{"get_commit", "commits", false, roleReporter, "Repository › Commit: Read"},
	{"compare", "commits", false, roleReporter, "Repository › Repository: Read"},
	{"get_file", "tree", false, roleReporter, "Repository › Repository: Read"},
	{"list_tree", "tree", false, roleReporter, "Repository › Repository: Read"},
	{"list_tags", "tags", false, roleReporter, "Repository › Repository Tag: Read"},
	{"list_merge_requests", "merge requests", false, roleReporter, "Repository › Merge Request: Read"},
	{"get_merge_request", "merge requests", false, roleReporter, "Repository › Merge Request: Read"},
	{"get_merge_request_changes", "merge requests", false, roleReporter, "Repository › Merge Request: Read"},
	{"list_mr_notes", "merge requests", false, roleReporter, "Project Planning › Work Item: Read"},
	{"list_mr_pipelines", "merge requests", false, roleReporter, "Repository › Merge Request: Read"},
	{"create_merge_request", "merge requests", true, roleDeveloper, "Repository › Merge Request: Create"},
	{"update_merge_request", "merge requests", true, roleDeveloper, "Repository › Merge Request: Update"},
	{"add_mr_note", "merge requests", true, roleReporter, "Project Planning › Work Item: Create"},
	{"approve_merge_request", "merge requests", true, roleDeveloper, "Repository › Merge Request: Approve"},
	{"merge_merge_request", "merge requests", true, roleDeveloper, "Repository › Merge Request: Merge"},
	{"list_pipelines", "pipelines", false, roleReporter, "CI/CD › Pipeline: Read"},
	{"get_pipeline", "pipelines", false, roleReporter, "CI/CD › Pipeline: Read"},
	{"list_pipeline_jobs", "pipelines", false, roleReporter, "CI/CD › Pipeline: Read"},
	{"get_job_log", "pipelines", false, roleReporter, "CI/CD › Job: Read"},
	{"retry_pipeline", "pipelines", true, roleDeveloper, "CI/CD › Pipeline: Update"},
	{"cancel_pipeline", "pipelines", true, roleDeveloper, "CI/CD › Pipeline: Update"},
	{"list_issues", "issues", false, roleGuest, "Project Planning › Work Item: Read"},
	{"get_issue", "issues", false, roleGuest, "Project Planning › Work Item: Read"},
	{"create_issue", "issues", true, roleGuest, "Project Planning › Work Item: Create"},
	{"add_issue_note", "issues", true, roleGuest, "Project Planning › Work Item: Create"},
}

type probe struct {
	Name   string `json:"name"`
	Status int    `json:"status"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

type opPermission struct {
	Op     string `json:"op"`
	Mark   string `json:"mark"`
	Needs  string `json:"needs"`
	Reason string `json:"reason"`
}

// PermissionReport is what the config page's checklist is drawn from.
type PermissionReport struct {
	User       string         `json:"user,omitempty"`
	TokenName  string         `json:"token_name,omitempty"`
	Scopes     []string       `json:"scopes"`
	ScopeInfo  string         `json:"scope_info"`
	ExpiresAt  string         `json:"expires_at,omitempty"`
	Project    string         `json:"project,omitempty"`
	Visibility string         `json:"visibility,omitempty"`
	Role       string         `json:"role,omitempty"`
	RoleInfo   string         `json:"role_info"`
	Probes     []probe        `json:"probes"`
	Ops        []opPermission `json:"ops"`
	Summary    string         `json:"summary"`
	// MissingRead / MissingWrite: fine-grained permissions to add in
	// GitLab, deduped, for the denied read and write ops.
	MissingRead  []string `json:"missing_read,omitempty"`
	MissingWrite []string `json:"missing_write,omitempty"`
}

func addOnce(list *[]string, v string) {
	for _, x := range *list {
		if x == v {
			return
		}
	}
	*list = append(*list, v)
}

// probeGET is one read-only call; a non-2xx answer is a result, not an error.
func probeGET(c *connector.Ctx, name, path string, q url.Values) (probe, any) {
	v, err := doJSON(c, http.MethodGet, apiURL(c, path, q), nil)
	if err == nil {
		return probe{Name: name, Status: http.StatusOK, OK: true}, v
	}
	var ae *apiError
	if errors.As(err, &ae) {
		return probe{Name: name, Status: ae.Status, Error: ae.Message}, nil
	}
	return probe{Name: name, Error: err.Error()}, nil
}

// evaluatePermissions reads what the token can do. Reads are probed live
// (one GET per resource family, so fine-grained tokens, whose permissions
// do not map to scopes, are judged by what GitLab actually answers). Writes
// are never performed: they are judged from the scopes and the role.
func evaluatePermissions(c *connector.Ctx, project string) (*PermissionReport, error) {
	rep := &PermissionReport{Scopes: []string{}}
	probes := map[string]probe{}
	add := func(p probe) {
		probes[p.Name] = p
		rep.Probes = append(rep.Probes, p)
	}
	one := url.Values{"per_page": {"1"}}

	up, me := probeGET(c, "user", "/user", nil)
	add(up)
	user, _ := me.(map[string]any)
	rep.User = str(user["username"])
	isAdmin, _ := user["is_admin"].(bool)
	if up.Status == 0 {
		return nil, fmt.Errorf("gitlab unreachable: %s", up.Error)
	}
	if up.Status == http.StatusUnauthorized {
		return nil, fmt.Errorf("token rejected (401): %s", up.Error)
	}
	pp, _ := probeGET(c, "projects", "/projects", url.Values{"membership": {"true"}, "per_page": {"1"}})
	add(pp)

	// The token describes itself (GitLab 15.5+). Fine-grained tokens and
	// older hosts refuse; the scopes then stay unknown.
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
		rep.ScopeInfo = "token does not describe itself (" + err.Error() + "); likely a fine-grained token. Reads are probed live, writes cannot be verified without performing them"
	}
	hasScopes := len(rep.Scopes) > 0

	role, roleKnown, public := roleNone, false, false
	if project != "" {
		rep.Project = project
		pid := "/projects/" + url.PathEscape(project)
		pr, p := probeGET(c, "project", pid, nil)
		add(pr)
		if pr.OK {
			pm, _ := p.(map[string]any)
			rep.Visibility = str(pm["visibility"])
			public = rep.Visibility == "public" || rep.Visibility == "internal"
		}
		// Probe every family even when the project itself is refused: a
		// fine-grained token can read files and commits without being
		// allowed to read the project's metadata.
		for _, f := range []struct{ name, path string }{
			{"branches", "/repository/branches"},
			{"commits", "/repository/commits"},
			{"tree", "/repository/tree"},
			{"tags", "/repository/tags"},
			{"merge requests", "/merge_requests"},
			{"pipelines", "/pipelines"},
			{"issues", "/issues"},
		} {
			r, _ := probeGET(c, f.name, pid+f.path, one)
			add(r)
		}
		// Role: members/all includes the ones inherited from parent groups,
		// which permissions.project_access leaves out.
		switch {
		case !pr.OK:
			rep.RoleInfo = "project cannot be read (" + probeText(pr) + ")" + projectHint(project, pr)
		case isAdmin:
			role, roleKnown = roleOwner, true
			rep.RoleInfo = "the token's user is an instance admin"
		case user == nil:
			rep.RoleInfo = "role unknown: the token may not read /user"
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
				rep.RoleInfo = "read from the project's members (inherited included)"
			case errors.As(err, &ae) && ae.Status == http.StatusNotFound:
				roleKnown = true
				rep.RoleInfo = "the token's user is not a member of this project"
			default:
				rep.RoleInfo = "role cannot be read: " + err.Error()
			}
		}
		if roleKnown {
			rep.Role = roleName(role)
		}
	} else {
		rep.RoleInfo = "fill default_project to probe a project and check the role"
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
		if !hasScopes {
			// A fine-grained token: name the permission as GitLab's form does.
			need[0] = n.Granular
		}
		p.Needs = strings.Join(need, ", ")
		pr, probed := probes[n.Family]

		switch {
		case !n.Write && probed && pr.OK:
			p.Mark, p.Reason = "✅", "live read of "+n.Family+" succeeded"
		case !n.Write && probed:
			p.Mark, p.Reason = "❌", "live read of "+n.Family+" refused: "+probeText(pr)
		case !n.Write:
			p.Mark, p.Reason = "❔", rep.RoleInfo
		case probed && !pr.OK:
			p.Mark, p.Reason = "❌", "the token cannot even read "+n.Family
		case hasScopes && !scopeCovers(granted, true):
			p.Mark, p.Reason = "❌", "token has no api scope"
		case project == "" || !roleKnown:
			p.Mark, p.Reason = "❔", "write op; "+rep.RoleInfo
		case role < roleNeeded:
			p.Mark, p.Reason = "❌", "role "+roleName(role)+" in "+project+", needs "+roleName(roleNeeded)
		case !hasScopes:
			p.Mark, p.Reason = "❔", "role ok; fine-grained/unknown token, write permission not verifiable without performing it"
		default:
			p.Mark, p.Reason = "✅", "api scope and role ok"
		}
		if n.Op == "merge_merge_request" && p.Mark == "✅" && role < roleMaintainer {
			p.Reason += "; a protected target branch may still need Maintainer"
		}
		switch p.Mark {
		case "✅":
			allowed++
		case "❌":
			denied++
			if !hasScopes && n.Granular != "" {
				if n.Write {
					addOnce(&rep.MissingWrite, n.Granular)
				} else {
					addOnce(&rep.MissingRead, n.Granular)
				}
			}
		default:
			unknown++
		}
		rep.Ops = append(rep.Ops, p)
	}
	rep.Summary = fmt.Sprintf("%d allowed, %d denied, %d unknown", allowed, denied, unknown)
	return rep, nil
}

// probeText is "<status> <message>" without repeating the status GitLab
// already puts in its message ("404 Not Found").
func probeText(p probe) string {
	code := strconv.Itoa(p.Status)
	if p.Status == 0 || strings.HasPrefix(p.Error, code) {
		return p.Error
	}
	return code + " " + p.Error
}

// projectHint explains a 404 on the project: GitLab answers 404 both for a
// path that does not exist and for a project the token may not see.
func projectHint(project string, p probe) string {
	if p.Status != http.StatusNotFound {
		return ""
	}
	if _, err := strconv.Atoi(project); err != nil && !strings.Contains(project, "/") {
		return ". \"" + project + "\" is only a namespace: use the full group/repo path"
	}
	return ". GitLab answers 404 for a wrong path and for a project this token may not see: check the path, then the token's access to it"
}

// scopeCovers: api grants everything, read_api only reads.
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
	// The test box overrides default_project for one check without saving it.
	project := strings.Trim(strings.TrimSpace(c.Input("project")), "/")
	if project == "" {
		project = strings.Trim(strings.TrimSpace(c.Cfg("default_project")), "/")
	}
	bar := permToolbar(project)
	if strings.TrimSpace(c.Cfg("token")) == "" {
		return map[string]any{"html": permCard(false, "Isi token dulu untuk mengecek permission.", bar, nil, nil)}, nil
	}
	rep, err := evaluatePermissions(c, project)
	if err != nil {
		return map[string]any{"html": permCard(false, html.EscapeString(err.Error()), bar, nil, nil)}, nil
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
	return map[string]any{"html": permCard(denied == 0, head, bar, tokenFacts(rep, time.Now()), rep.Ops)}, nil
}

// permToolbar: a project box to test any path without saving it, and a
// recheck that runs the probes again (after a token or permission change).
// type="button": a bare <button> submits the surrounding config form.
func permToolbar(project string) string {
	const btn = `cursor:pointer;font-size:12px;font-weight:500;height:28px;padding:0 12px;border-radius:8px;border:1px solid currentColor;background:transparent`
	return `<div style="display:flex;gap:8px;flex-wrap:wrap;margin-top:8px">` +
		`<input name="project" value="` + html.EscapeString(project) + `" placeholder="group/repo" ` +
		`style="flex:1 1 240px;height:28px;padding:0 8px;font-size:12px;font-family:ui-monospace,monospace;border:1px solid currentColor;border-radius:8px;background:transparent;color:inherit;opacity:.9"/>` +
		`<button type="button" data-op="permission_status" style="` + btn + `">Test project</button>` +
		`<button type="button" data-op="permission_status" style="` + btn + `">↻ Recheck</button></div>` +
		`<div class="text-xs opacity-70" style="margin-top:4px">Test project checks the path typed here without saving it; Recheck runs every probe again with the saved token.</div>`
}

// tokenFacts are the lines above the checklist: who, which scopes, until
// when, and the role in default_project.
func tokenFacts(rep *PermissionReport, now time.Time) []string {
	var out []string
	who := "user <b>" + html.EscapeString(rep.User) + "</b>"
	if rep.User == "" {
		who = "user: unknown (token may not read /user)"
	}
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
		if rep.Role != "" {
			line += ": role <b>" + html.EscapeString(rep.Role) + "</b> — " + html.EscapeString(rep.RoleInfo)
		} else {
			line += ": " + html.EscapeString(rep.RoleInfo)
		}
		out = append(out, line)
	} else {
		out = append(out, html.EscapeString(rep.RoleInfo))
	}
	if len(rep.MissingRead) > 0 {
		out = append(out, "<b>Add to the token in GitLab (read):</b> "+html.EscapeString(strings.Join(rep.MissingRead, " · ")))
	}
	if len(rep.MissingWrite) > 0 {
		out = append(out, "<b>Only if wick should also write:</b> "+html.EscapeString(strings.Join(rep.MissingWrite, " · ")))
	}
	return out
}

func permCard(ok bool, head, toolbar string, facts []string, ops []opPermission) string {
	ring, text, dot := "border-neg-300 bg-neg-100 dark:bg-navy-800", "text-neg-400", "bg-neg-400"
	if ok {
		ring, text, dot = "border-pos-300 bg-pos-100 dark:bg-navy-800", "text-pos-400", "bg-pos-400"
	}
	var b strings.Builder
	b.WriteString(`<div class="rounded-lg border ` + ring + ` px-4 py-2.5">`)
	b.WriteString(`<div class="flex items-center gap-2"><span class="h-2 w-2 rounded-full ` + dot + `"></span>`)
	b.WriteString(`<span class="text-xs font-medium ` + text + `">` + head + `</span></div>`)
	b.WriteString(toolbar)
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
