package main

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/yogasw/wick/pkg/connector"
)

// ── Token & access panel ─────────────────────────────────────────────
//
// token_access backs the `html=token_access` widget on the config page. It
// shows who the stored token authenticates as, what kind of token it is and
// which scopes it carries, the instance's default repository and what each
// operation category needs. Opening the page costs one GET /user; the
// validate_repo_setup checks only run when Validate setup is pressed, under
// one overall deadline.
//
// Read-only. The token value is never rendered — at most its well-known
// prefix (ghp_, github_pat_, …).

// TokenAccessInput: Browser is the field value on page load and the
// clicked element's data-arg afterwards — "validate" runs the setup checks.
// Owner and Repo are the panel's own form controls, posted back on any
// data-op click.
type TokenAccessInput struct {
	Browser string `wick:"desc=validate runs the setup checks. Anything else only shows the token."`
	Owner   string `wick:"desc=Repository owner to validate. Default: default_owner."`
	Repo    string `wick:"desc=Repository name to validate. Default: default_repo."`
}

// Theme tokens. The manager's palette is exposed as "R G B" triplets, so
// every colour goes through rgb(); the dark variants hang off html.dark.
// No Tailwind class is used — Go strings are not scanned, so they would be
// purged from the built CSS.
const (
	taText     = "rgb(var(--color-black-900))"
	taTextDark = "rgb(var(--color-white-100))"
	taMuted    = "rgb(var(--color-black-700))"
	taMutedDk  = "rgb(var(--color-black-600))"
	taBorder   = "rgb(var(--color-white-300))"
	taBorderDk = "rgb(var(--color-navy-600))"
	taPos      = "#27B199"
	taNeg      = "#E5484D"
	taCau      = "#D4A72C"
)

// tokenInfo is what GET /user and the token prefix tell us.
type tokenInfo struct {
	Set     bool
	Kind    string // human label of the token type
	Prefix  string
	Classic bool // classic PAT / OAuth token: X-OAuth-Scopes is meaningful
	Login   string
	Name    string
	Type    string // User / Bot / Organization
	Scopes  []string
	Expires string
	Err     string
}

// tokenKinds maps GitHub's documented token prefixes to a label. Order
// matters: github_pat_ must be tested before the three-letter prefixes.
var tokenKinds = []struct {
	prefix, label string
	classic       bool
}{
	{"github_pat_", "fine-grained personal access token", false},
	{"ghp_", "classic personal access token", true},
	{"gho_", "OAuth app token", true},
	{"ghu_", "GitHub App user token", false},
	{"ghs_", "GitHub App installation token", false},
}

func probeGitHubToken(c *connector.Ctx) tokenInfo {
	t := tokenInfo{}
	token := strings.TrimSpace(c.Cfg("token"))
	if token == "" {
		return t
	}
	t.Set = true
	t.Kind = "unrecognised token type"
	for _, k := range tokenKinds {
		if strings.HasPrefix(token, k.prefix) {
			t.Prefix, t.Kind, t.Classic = k.prefix, k.label, k.classic
			break
		}
	}
	raw, header, err := doRequestHeaders(c, "GET", buildURL(c, "/user"), nil)
	if err != nil {
		// Never echo a token back, even if an upstream error quoted it.
		t.Err = strings.ReplaceAll(err.Error(), token, "[redacted]")
		return t
	}
	m, _ := raw.(map[string]any)
	t.Login, t.Name, t.Type = str(m, "login"), str(m, "name"), str(m, "type")
	t.Scopes = scopeList(header)
	if header != nil {
		t.Expires = header.Get("Github-Authentication-Token-Expiration")
		// A classic-looking token without the header is not classic after all.
		if t.Prefix == "" && header.Get("X-Oauth-Scopes") != "" {
			t.Classic = true
		}
	}
	return t
}

func scopeList(h http.Header) []string {
	if h == nil {
		return nil
	}
	var out []string
	for _, s := range strings.Split(h.Get("X-Oauth-Scopes"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// categoryNeed is what one operation category asks of the token. Scopes is
// a list of classic scopes, any one of which is enough; empty means no scope
// is needed for public data.
type categoryNeed struct {
	Scopes      []string
	FineGrained string
}

var categoryNeeds = map[string]categoryNeed{
	"Common Actions":        {[]string{"repo"}, "Contents, Issues, Pull requests: Read and write"},
	"Repositories":          {nil, "Metadata: Read (Starring is user-level)"},
	"Issues":                {[]string{"repo"}, "Issues: Read and write"},
	"Pull Requests":         {[]string{"repo"}, "Pull requests: Read and write"},
	"Releases":              {[]string{"repo"}, "Contents: Read and write"},
	"Tags":                  {nil, "Contents: Read"},
	"User":                  {nil, "none"},
	"Comments":              {[]string{"repo"}, "Issues / Pull requests: Read and write"},
	"Pull Request Reviews":  {[]string{"repo"}, "Pull requests: Read and write"},
	"Branches":              {[]string{"repo"}, "Contents: Read and write"},
	"Labels & Assignees":    {[]string{"repo"}, "Issues: Read and write"},
	"Commits":               {nil, "Contents: Read"},
	"Search":                {nil, "Metadata: Read"},
	"Repository Management": {[]string{"repo"}, "Administration: Read and write"},
	"Actions":               {[]string{"repo"}, "Actions: Read and write"},
	"Webhooks":              {[]string{"admin:repo_hook", "repo"}, "Webhooks: Read and write"},
	"Repository Settings":   {[]string{"repo"}, "Administration: Read, Environments: Read (bypass lists also need the admin role on the repo)"},
}

// scopeImplied: holding the key scope grants the listed ones too.
var scopeImplied = map[string][]string{
	"repo":            {"public_repo", "repo:status", "repo_deployment"},
	"admin:repo_hook": {"write:repo_hook", "read:repo_hook"},
}

func hasScope(have []string, want string) bool {
	for _, h := range have {
		if h == want {
			return true
		}
		for _, imp := range scopeImplied[h] {
			if imp == want {
				return true
			}
		}
	}
	return false
}

// panelValidateArg is the data-arg of the Validate setup button.
const panelValidateArg = "validate"

// panelValidateTimeout bounds the whole validation run from the panel, so a
// slow or unreachable API cannot hang the config page. A var for tests.
var panelValidateTimeout = 20 * time.Second

func tokenAccess(c *connector.Ctx) (any, error) {
	t := probeGitHubToken(c)
	owner := firstNonEmpty(strings.TrimSpace(c.Input("owner")), strings.TrimSpace(c.Cfg("default_owner")))
	repo := firstNonEmpty(strings.TrimSpace(c.Input("repo")), strings.TrimSpace(c.Cfg("default_repo")))
	var report *setupReport
	var note string
	if c.Input("browser") == panelValidateArg && t.Set && t.Err == "" {
		switch {
		case owner == "" || repo == "":
			note = "Enter both owner and repo."
		case checkRepoName(owner, repo) != nil:
			note = checkRepoName(owner, repo).Error()
		default:
			report = validateWithDeadline(c, owner, repo)
		}
	}
	return map[string]any{"html": renderTokenAccess(c, t, owner, repo, report, note)}, nil
}

// validateWithDeadline runs the checks on a copy of the call whose context
// expires after panelValidateTimeout; requests cut off by it fail their
// check, and a final line says the run was cut short.
func validateWithDeadline(c *connector.Ctx, owner, repo string) *setupReport {
	ctx, cancel := context.WithTimeout(c.Context(), panelValidateTimeout)
	defer cancel()
	sub := connector.NewCtx(ctx, c.InstanceID(), c.Configs(), c.Inputs(), c.HTTP, nil, nil)
	report := runSetupValidation(sub, owner, repo)
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		report.add("Token", "Finished in time", statusFail, fmt.Sprintf("timed out after %s; the checks that were still waiting failed", panelValidateTimeout))
	}
	return report
}

func renderTokenAccess(c *connector.Ctx, t tokenInfo, owner, repo string, report *setupReport, note string) string {
	const p = "wgta" // class prefix: this markup lands inside the manager page
	esc := html.EscapeString
	var b strings.Builder

	fmt.Fprintf(&b, `<style>
.%[1]s-w{color:%[2]s;font-size:13px;line-height:1.45}
html.dark .%[1]s-w{color:%[3]s}
.%[1]s-top{display:flex;align-items:center;margin-bottom:8px}
.%[1]s-re{margin-left:auto;font-size:11px;color:%[4]s;text-decoration:underline;cursor:pointer}
html.dark .%[1]s-re,html.dark .%[1]s-mut,html.dark .%[1]s-kv span:nth-child(odd),html.dark .%[1]s-w th{color:%[5]s}
.%[1]s-use{display:flex;gap:8px;align-items:flex-start;padding:9px 11px;border-radius:8px;margin-bottom:10px;border:1px solid %[8]s;background:%[8]s1f}
.%[1]s-use.bad{border-color:%[9]s;background:%[9]s1f}
.%[1]s-card{border:1px solid %[6]s;border-radius:8px;padding:9px 11px;font-size:12.5px;margin-bottom:10px}
html.dark .%[1]s-card,html.dark .%[1]s-w td,html.dark .%[1]s-w th,html.dark .%[1]s-in{border-color:%[7]s}
.%[1]s-card h4{margin:0 0 6px;font:600 12px system-ui,sans-serif}
.%[1]s-kv{display:grid;grid-template-columns:110px 1fr;gap:2px 8px}
.%[1]s-kv span:nth-child(odd){color:%[4]s}
.%[1]s-pill{border-radius:999px;padding:1px 7px;font:700 10px system-ui,sans-serif;white-space:nowrap}
.%[1]s-pos{background:%[8]s24;color:%[8]s}.%[1]s-neg{background:%[9]s21;color:%[9]s}
.%[1]s-cau{background:%[10]s24;color:%[10]s}.%[1]s-pm{background:rgba(139,148,167,.15);color:%[4]s}
.%[1]s-mut{color:%[4]s;font-size:11.5px}
.%[1]s-w table{width:100%%;border-collapse:collapse;font-size:12.5px;margin-bottom:10px}
.%[1]s-w th{text-align:left;font-weight:600;color:%[4]s;padding:5px 8px;border-bottom:1px solid %[6]s}
.%[1]s-w td{padding:5px 8px;border-bottom:1px solid %[6]s;vertical-align:top}
.%[1]s-w tr.cat td{font-weight:600;background:rgba(127,127,127,.06)}
.%[1]s-w code{font-size:12px}
.%[1]s-form{display:flex;gap:6px;align-items:center;flex-wrap:wrap;margin:6px 0 10px}
.%[1]s-in{border:1px solid %[6]s;border-radius:6px;padding:4px 7px;font-size:12.5px;background:transparent;color:inherit;min-width:140px}
.%[1]s-btn{border:1px solid %[8]s;border-radius:6px;padding:4px 10px;font-size:12px;background:%[8]s1f;color:inherit;cursor:pointer}
</style><div class="%[1]s-w">`, p, taText, taTextDark, taMuted, taMutedDk, taBorder, taBorderDk, taPos, taNeg, taCau)

	fmt.Fprintf(&b, `<div class="%[1]s-top"><a href="#" class="%[1]s-re" data-op="token_access">↻ Recheck</a></div>`, p)

	// Identity banner.
	switch {
	case !t.Set:
		fmt.Fprintf(&b, `<div class="%s-use bad">⚠️ <span>No token is stored — every operation on this instance fails until <code>token</code> is filled.</span></div>`, p)
	case t.Err != "":
		fmt.Fprintf(&b, `<div class="%s-use bad">⚠️ <span>The stored token failed <code>GET /user</code>: %s</span></div>`, p, esc(t.Err))
	default:
		who := t.Login
		if t.Name != "" {
			who += " (" + t.Name + ")"
		}
		fmt.Fprintf(&b, `<div class="%s-use">✅ <span>Operations on this instance run as <b>%s</b> — a %s.</span></div>`, p, esc(who), esc(t.Kind))
	}

	// Token card.
	if t.Set {
		fmt.Fprintf(&b, `<div class="%[1]s-card"><h4>Token</h4><div class="%[1]s-kv">`, p)
		typ := t.Kind
		if t.Prefix != "" {
			typ += " (" + t.Prefix + "…)"
		}
		fmt.Fprintf(&b, `<span>Type</span><span>%s</span>`, esc(typ))
		if t.Err == "" {
			fmt.Fprintf(&b, `<span>Account</span><span>%s</span>`, esc(firstNonEmpty(t.Login, "—")+" · "+firstNonEmpty(t.Type, "—")))
		}
		switch {
		case t.Err != "":
		case t.Classic && len(t.Scopes) > 0:
			fmt.Fprintf(&b, `<span>Scopes</span><span><code>%s</code></span>`, esc(strings.Join(t.Scopes, ", ")))
		case t.Classic:
			fmt.Fprintf(&b, `<span>Scopes</span><span>none granted (public data only)</span>`)
		default:
			fmt.Fprintf(&b, `<span>Permissions</span><span>GitHub does not report a fine-grained or app token's permissions; the probe under Validate setup shows what it can read.</span>`)
		}
		if t.Expires != "" {
			fmt.Fprintf(&b, `<span>Expires</span><span>%s</span>`, esc(t.Expires))
		}
		base := strings.TrimSpace(c.Cfg("base_url"))
		fmt.Fprintf(&b, `<span>API</span><span>%s</span>`, esc(firstNonEmpty(base, defaultBaseURL)))
		dr := "—"
		if o, r := strings.TrimSpace(c.Cfg("default_owner")), strings.TrimSpace(c.Cfg("default_repo")); o != "" || r != "" {
			dr = firstNonEmpty(o, "?") + "/" + firstNonEmpty(r, "?")
		}
		fmt.Fprintf(&b, `<span>Default repo</span><span>%s</span>`, esc(dr))
		b.WriteString(`</div></div>`)
	}

	writeCategoryTable(&b, p, t)
	writeValidateSection(&b, p, t, owner, repo, report, note)

	b.WriteString(`</div>`)
	return b.String()
}

// writeCategoryTable lists what each operation category needs. The "This
// token" column is only judged for classic tokens, whose scopes GitHub
// reports; fine-grained tokens say so instead of guessing.
func writeCategoryTable(b *strings.Builder, p string, t tokenInfo) {
	esc := html.EscapeString
	judge := t.Set && t.Err == "" && t.Classic
	b.WriteString(`<table><thead><tr><th>Category</th><th>Operations</th><th>Classic scope</th><th>Fine-grained permission</th>`)
	if judge {
		b.WriteString(`<th>This token</th>`)
	}
	b.WriteString(`</tr></thead><tbody>`)
	for _, cat := range Operations() {
		read, write := 0, 0
		for _, op := range cat.Ops {
			switch {
			case op.ConfigOnly:
			case op.Destructive:
				write++
			default:
				read++
			}
		}
		if read+write == 0 {
			continue
		}
		need := categoryNeeds[cat.Title]
		scope := "none for public repos"
		if len(need.Scopes) > 0 {
			parts := make([]string, len(need.Scopes))
			for i, s := range need.Scopes {
				parts[i] = "<code>" + esc(s) + "</code>"
			}
			scope = strings.Join(parts, " or ")
		}
		ops := fmt.Sprintf("%d read", read)
		if write > 0 {
			ops += fmt.Sprintf(", %d write", write)
		}
		fmt.Fprintf(b, `<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td>`, esc(cat.Title), ops, scope, esc(firstNonEmpty(need.FineGrained, "—")))
		if judge {
			ok := len(need.Scopes) == 0
			for _, s := range need.Scopes {
				if hasScope(t.Scopes, s) {
					ok = true
				}
			}
			if ok {
				fmt.Fprintf(b, `<td><span class="%[1]s-pill %[1]s-pos">ok</span></td>`, p)
			} else {
				fmt.Fprintf(b, `<td><span class="%[1]s-pill %[1]s-neg">missing scope</span></td>`, p)
			}
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table>`)
}

func writeValidateSection(b *strings.Builder, p string, t tokenInfo, owner, repo string, report *setupReport, note string) {
	esc := html.EscapeString
	fmt.Fprintf(b, `<div class="%[1]s-card"><h4>Validate setup</h4>`+
		`<div class="%[1]s-mut">Checks the repository's master, develop and tag rulesets, the %[2]s environment, and what this token can read. Read-only.</div>`+
		`<div class="%[1]s-form"><input class="%[1]s-in" name="owner" placeholder="owner" value="%[3]s"/>`+
		`<span>/</span><input class="%[1]s-in" name="repo" placeholder="repo" value="%[4]s"/>`+
		`<button type="button" class="%[1]s-btn" data-op="token_access" data-arg="%[5]s">Validate setup</button></div>`,
		p, releaseEnvironment, esc(owner), esc(repo), panelValidateArg)

	switch {
	case !t.Set || t.Err != "":
		fmt.Fprintf(b, `<div class="%s-mut">Fix the token first; validation needs a working token.</div>`, p)
	case note != "":
		fmt.Fprintf(b, `<div class="%s-mut">%s</div>`, p, esc(note))
	case report == nil:
		fmt.Fprintf(b, `<div class="%s-mut">Enter a repository (or set default_owner and default_repo) and press Validate setup. About ten read-only requests, stopped after %s.</div>`, p, panelValidateTimeout)
	default:
		fmt.Fprintf(b, `<div class="%[1]s-mut" style="margin-bottom:6px">%[2]s/%[3]s: %[4]d passed, %[5]d failed, %[6]d warnings.</div>`,
			p, esc(report.Owner), esc(report.Repo), report.Passed, report.Failed, report.Warned)
		b.WriteString(`<table><thead><tr><th>Check</th><th>Result</th><th>Reason</th></tr></thead><tbody>`)
		group := ""
		for _, ch := range report.Checks {
			if ch.Group != group {
				group = ch.Group
				fmt.Fprintf(b, `<tr class="cat"><td colspan="3">%s</td></tr>`, esc(group))
			}
			cls := p + "-pos"
			switch ch.Status {
			case statusFail:
				cls = p + "-neg"
			case statusWarn:
				cls = p + "-cau"
			}
			fmt.Fprintf(b, `<tr><td>%s</td><td><span class="%s-pill %s">%s</span></td><td>%s</td></tr>`,
				esc(ch.Name), p, cls, esc(ch.Status), esc(ch.Reason))
		}
		b.WriteString(`</tbody></table>`)
	}
	b.WriteString(`</div>`)
}
