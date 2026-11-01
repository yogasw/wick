// rulesedit.go implements the "Merge rules" editor on the config page. The
// merge_rules row itself is hidden: a kvlist can only show text boxes, so an
// approver would have to be typed as a raw account_id. The editor shows mode as
// a dropdown and lets the operator find an approver by NAME among the
// workspace's Bitbucket users, while still storing the account_id the rule
// check matches on.
//
// Every button saves. The rows are rebuilt from the controls the click posted
// (named r_<field>_<index>), never from c.Cfg, because the {fields} write has
// not committed yet when the op returns and reading config would redraw the
// previous state.
package main

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/yogasw/wick/pkg/connector"
)

// RulesEditorInput drives the editor. browser carries data-arg by the html=
// convention. The per-row controls are named r_*_<index> and are read with
// c.Input directly, since the index is only known at render time.
type RulesEditorInput struct {
	Browser  string `wick:"desc=data-arg of the clicked button, supplied by the config UI."`
	RowCount string `wick:"key=row_count;desc=Number of rule rows rendered."`
}

// editorRow is one rule as the editor holds it. Names parallels Approvers so
// the editor can show a person instead of an id; the rule check ignores it.
type editorRow struct {
	Repo      string
	Mode      string
	Min       string
	Approvers []string
	Names     []string
}

// bbUser is a Bitbucket account found by the approver search.
type bbUser struct {
	AccountID string
	Name      string
	Nickname  string
}

// rowsFromConfig reads the saved rules for the first render.
func rowsFromConfig(raw string) []editorRow {
	var rows []map[string]string
	_ = json.Unmarshal([]byte(strings.TrimSpace(raw)), &rows)
	out := make([]editorRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, editorRow{
			Repo:      r["repo"],
			Mode:      defaultString(r["mode"], "allow"),
			Min:       r["min_approvals"],
			Approvers: splitList(r["required_approvers"], ","),
			Names:     splitList(r["approver_names"], "|"),
		})
	}
	return out
}

// rowsFromInput rebuilds the rows from the controls the click posted.
func rowsFromInput(c *connector.Ctx) []editorRow {
	n, _ := strconv.Atoi(c.Input("row_count"))
	out := make([]editorRow, 0, n)
	for i := 0; i < n; i++ {
		k := strconv.Itoa(i)
		out = append(out, editorRow{
			Repo:      strings.TrimSpace(c.Input("r_repo_" + k)),
			Mode:      defaultString(c.Input("r_mode_"+k), "allow"),
			Min:       strings.TrimSpace(c.Input("r_min_" + k)),
			Approvers: splitList(c.Input("r_appr_"+k), ","),
			Names:     splitList(c.Input("r_names_"+k), "|"),
		})
	}
	return out
}

func splitList(s, sep string) []string {
	var out []string
	for _, p := range strings.Split(s, sep) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// encodeRows turns the rows into the merge_rules value. A row without a repo
// is dropped (a fresh "Add rule" row nobody filled in yet).
func encodeRows(rows []editorRow) (string, error) {
	out := make([]map[string]string, 0, len(rows))
	for _, r := range rows {
		if strings.TrimSpace(r.Repo) == "" {
			continue
		}
		names := make([]string, len(r.Approvers))
		for i := range r.Approvers {
			if i < len(r.Names) {
				names[i] = r.Names[i]
			}
		}
		out = append(out, map[string]string{
			"repo":               r.Repo,
			"mode":               r.Mode,
			"min_approvals":      r.Min,
			"required_approvers": strings.Join(r.Approvers, ","),
			"approver_names":     strings.Join(names, "|"),
		})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	// The same parser merge_pull_request uses: whatever it refuses is not saved.
	if _, err := parseMergeRules(string(b)); err != nil {
		return "", err
	}
	return string(b), nil
}

// editorState is everything one render needs besides the rows.
type editorState struct {
	Notice   string
	NoticeOK bool
	Search   int // row whose search results are shown, -1 for none
	Query    string
	Results  []bbUser
}

// save stores rows and re-renders. An invalid set is not written, so a typo
// cannot replace a working rule set.
func saveRows(rows []editorRow, st editorState, okMsg string) (any, error) {
	encoded, err := encodeRows(rows)
	if err != nil {
		st.Notice, st.NoticeOK = "Not saved: "+err.Error(), false
		return map[string]any{"html": renderRulesEditor(rows, st)}, nil
	}
	if st.Notice == "" {
		st.Notice, st.NoticeOK = okMsg, true
	}
	return map[string]any{
		"html":   renderRulesEditor(rows, st),
		"fields": map[string]string{"merge_rules": encoded},
	}, nil
}

func rulesEditor(c *connector.Ctx) (any, error) {
	rows := rowsFromConfig(c.Cfg("merge_rules"))
	fillNames(c, rows)
	return map[string]any{"html": renderRulesEditor(rows, editorState{Search: -1})}, nil
}

// fillNames looks up the display name of an approver saved without one (a
// rule typed by hand, or one saved before the editor existed). A failed lookup
// leaves the id showing; it never blocks the render.
func fillNames(c *connector.Ctx, rows []editorRow) {
	cache := map[string]string{}
	for i := range rows {
		for len(rows[i].Names) < len(rows[i].Approvers) {
			rows[i].Names = append(rows[i].Names, "")
		}
		for j, id := range rows[i].Approvers {
			if rows[i].Names[j] != "" {
				continue
			}
			name, ok := cache[id]
			if !ok {
				if u, err := resourceURL(c, "users", id); err == nil {
					if raw, err := fetchJSON(c, requestParams{Method: http.MethodGet, URL: u}); err == nil {
						m, _ := raw.(map[string]any)
						name, _ = m["display_name"].(string)
					}
				}
				cache[id] = name
			}
			rows[i].Names[j] = name
		}
	}
}

func rulesEditorSave(c *connector.Ctx) (any, error) {
	return saveRows(rowsFromInput(c), editorState{Search: -1}, "Rules saved.")
}

func rulesEditorAddRow(c *connector.Ctx) (any, error) {
	rows := append(rowsFromInput(c), editorRow{Mode: "allow", Min: "1"})
	return map[string]any{"html": renderRulesEditor(rows, editorState{Search: -1, Notice: "New row added. Fill in repo, then Save.", NoticeOK: true})}, nil
}

func rulesEditorDeleteRow(c *connector.Ctx) (any, error) {
	rows := rowsFromInput(c)
	i, err := strconv.Atoi(c.Input("browser"))
	if err != nil || i < 0 || i >= len(rows) {
		return map[string]any{"html": renderRulesEditor(rows, editorState{Search: -1, Notice: "Row not found.", NoticeOK: false})}, nil
	}
	rows = append(rows[:i], rows[i+1:]...)
	return saveRows(rows, editorState{Search: -1}, "Rule deleted.")
}

// rulesEditorSearch looks up Bitbucket users by name for one row.
func rulesEditorSearch(c *connector.Ctx) (any, error) {
	rows := rowsFromInput(c)
	i, _ := strconv.Atoi(c.Input("browser"))
	q := strings.TrimSpace(c.Input("q_" + strconv.Itoa(i)))
	st := editorState{Search: i, Query: q}
	if q == "" {
		// Cleared while typing: just drop the results, no nagging.
		return map[string]any{"html": renderRulesEditor(rows, editorState{Search: -1})}, nil
	}
	users, source, err := searchUsers(c, q)
	switch {
	case err != nil:
		st.Notice = err.Error()
	case len(users) == 0:
		st.Notice = fmt.Sprintf("No Bitbucket user matches %q. Searched: %s.", q, source)
	default:
		st.Notice, st.NoticeOK = fmt.Sprintf("%d match(es) for %q. Searched: %s.", len(users), q, source), true
	}
	st.Results = users
	return map[string]any{"html": renderRulesEditor(rows, st)}, nil
}

// rulesEditorAddApprover takes data-arg "<row>|<account_id>|<name>".
func rulesEditorAddApprover(c *connector.Ctx) (any, error) {
	rows := rowsFromInput(c)
	parts := strings.SplitN(c.Input("browser"), "|", 3)
	i, err := strconv.Atoi(parts[0])
	if err != nil || len(parts) < 3 || i < 0 || i >= len(rows) {
		return map[string]any{"html": renderRulesEditor(rows, editorState{Search: -1, Notice: "Could not add that user.", NoticeOK: false})}, nil
	}
	id, name := parts[1], parts[2]
	for _, have := range rows[i].Approvers {
		if have == id {
			return saveRows(rows, editorState{Search: -1}, name+" is already required.")
		}
	}
	// Pad Names so it stays aligned with Approvers.
	for len(rows[i].Names) < len(rows[i].Approvers) {
		rows[i].Names = append(rows[i].Names, "")
	}
	rows[i].Approvers = append(rows[i].Approvers, id)
	rows[i].Names = append(rows[i].Names, name)
	return saveRows(rows, editorState{Search: -1}, name+" added and saved.")
}

// rulesEditorRemoveApprover takes data-arg "<row>|<account_id>".
func rulesEditorRemoveApprover(c *connector.Ctx) (any, error) {
	rows := rowsFromInput(c)
	parts := strings.SplitN(c.Input("browser"), "|", 2)
	i, err := strconv.Atoi(parts[0])
	if err != nil || len(parts) < 2 || i < 0 || i >= len(rows) {
		return map[string]any{"html": renderRulesEditor(rows, editorState{Search: -1, Notice: "Could not remove that user.", NoticeOK: false})}, nil
	}
	var ids, names []string
	for j, id := range rows[i].Approvers {
		if id == parts[1] {
			continue
		}
		ids = append(ids, id)
		if j < len(rows[i].Names) {
			names = append(names, rows[i].Names[j])
		} else {
			names = append(names, "")
		}
	}
	rows[i].Approvers, rows[i].Names = ids, names
	return saveRows(rows, editorState{Search: -1}, "Approver removed and saved.")
}

// searchUsers finds Bitbucket users whose name or nickname contains q. The
// workspace member list is the complete source, but it needs the
// read:workspace scope; a token without it falls back to the people seen on
// recent pull requests (authors, reviewers, approvers), which only needs the
// pull request scope the connector already uses.
func searchUsers(c *connector.Ctx, q string) ([]bbUser, string, error) {
	ws, err := workspace(c)
	if err != nil {
		return nil, "", err
	}
	users, err := workspaceMembers(c, ws)
	source := "workspace members"
	if err != nil {
		var ferr error
		users, ferr = recentPRPeople(c, ws)
		if ferr != nil {
			return nil, "", fmt.Errorf("could not list Bitbucket users of %s: %v (fallback from recent pull requests also failed: %v)", ws, err, ferr)
		}
		source = "people on recent pull requests (the API token cannot read the workspace member list: it needs read:workspace:bitbucket)"
	}
	needle := strings.ToLower(q)
	var out []bbUser
	for _, u := range users {
		if strings.Contains(strings.ToLower(u.Name), needle) || strings.Contains(strings.ToLower(u.Nickname), needle) {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(a, b int) bool { return strings.ToLower(out[a].Name) < strings.ToLower(out[b].Name) })
	if len(out) > 20 {
		out = out[:20]
	}
	return out, source, nil
}

// workspaceMembers pages through the workspace member list.
func workspaceMembers(c *connector.Ctx, ws string) ([]bbUser, error) {
	base, err := resourceURL(c, "workspaces", ws, "members")
	if err != nil {
		return nil, err
	}
	set := userSet{}
	next := withQuery(base, url.Values{"pagelen": {"100"}})
	for page := 0; next != "" && page < 10; page++ {
		raw, err := fetchJSON(c, requestParams{Method: http.MethodGet, URL: next})
		if err != nil {
			return nil, err
		}
		body, _ := raw.(map[string]any)
		values, _ := body["values"].([]any)
		for _, item := range values {
			m, _ := item.(map[string]any)
			u, _ := m["user"].(map[string]any)
			set.add(u)
		}
		next, _ = body["next"].(string)
	}
	return set.list(), nil
}

// recentPRPeople collects everyone on the pull requests of the most recently
// updated repositories. Repositories are read in parallel, a few at a time;
// one repository failing does not fail the search.
func recentPRPeople(c *connector.Ctx, ws string) ([]bbUser, error) {
	reposURL, err := resourceURL(c, "repositories", ws)
	if err != nil {
		return nil, err
	}
	raw, err := fetchJSON(c, requestParams{Method: http.MethodGet, URL: withQuery(reposURL, url.Values{
		"sort": {"-updated_on"}, "pagelen": {"25"}, "fields": {"values.slug"},
	})})
	if err != nil {
		return nil, err
	}
	body, _ := raw.(map[string]any)
	values, _ := body["values"].([]any)
	var slugs []string
	for _, item := range values {
		m, _ := item.(map[string]any)
		if slug, _ := m["slug"].(string); slug != "" {
			slugs = append(slugs, slug)
		}
	}

	var mu sync.Mutex
	set := userSet{}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	for _, slug := range slugs {
		u, err := resourceURL(c, "repositories", ws, slug, "pullrequests")
		if err != nil {
			continue
		}
		u = withQuery(u, url.Values{
			"state":   {"OPEN", "MERGED", "DECLINED"},
			"pagelen": {"50"},
			"fields":  {"values.author,values.reviewers,values.participants.user"},
		})
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			raw, err := fetchJSON(c, requestParams{Method: http.MethodGet, URL: u})
			if err != nil {
				return
			}
			body, _ := raw.(map[string]any)
			prs, _ := body["values"].([]any)
			mu.Lock()
			defer mu.Unlock()
			for _, item := range prs {
				pr, _ := item.(map[string]any)
				if pr == nil {
					continue
				}
				a, _ := pr["author"].(map[string]any)
				set.add(a)
				rs, _ := pr["reviewers"].([]any)
				for _, r := range rs {
					m, _ := r.(map[string]any)
					set.add(m)
				}
				ps, _ := pr["participants"].([]any)
				for _, p := range ps {
					m, _ := p.(map[string]any)
					u, _ := m["user"].(map[string]any)
					set.add(u)
				}
			}
		}(u)
	}
	wg.Wait()
	return set.list(), nil
}

// userSet dedupes Bitbucket user objects by account_id.
type userSet map[string]bbUser

func (s userSet) add(u map[string]any) {
	if u == nil {
		return
	}
	user := bbUser{}
	user.AccountID, _ = u["account_id"].(string)
	user.Name, _ = u["display_name"].(string)
	user.Nickname, _ = u["nickname"].(string)
	if user.AccountID == "" {
		return
	}
	s[user.AccountID] = user
}

func (s userSet) list() []bbUser {
	out := make([]bbUser, 0, len(s))
	for _, u := range s {
		out = append(out, u)
	}
	return out
}

func renderRulesEditor(rows []editorRow, st editorState) string {
	const p = "bbre"
	esc := html.EscapeString
	var b strings.Builder
	b.WriteString("<style>" + themeCSS(p))
	fmt.Fprintf(&b, `.%[1]s-w{color:%[2]s;font-size:13px;line-height:1.4}
.%[1]s-card{border:1px solid %[3]s;border-radius:12px;padding:16px;margin-bottom:12px;background:%[4]s}
.%[1]s-grid{display:grid;grid-template-columns:minmax(0,2fr) minmax(0,1fr) minmax(0,1fr) auto;gap:12px;align-items:end}
@media (max-width:640px){.%[1]s-grid{grid-template-columns:1fr 1fr}}
.%[1]s-l{display:block;font-size:11px;font-weight:500;color:var(--bb-muted);margin-bottom:4px;letter-spacing:.02em;text-transform:uppercase}
.%[1]s-in{width:100%%;box-sizing:border-box;font-family:inherit;font-size:13px;height:32px;padding:0 8px;border:1px solid %[3]s;border-radius:8px;background:%[7]s;color:%[2]s}
.%[1]s-mono{font-family:ui-monospace,SFMono-Regular,Menlo,monospace}
.%[1]s-btn{cursor:pointer;font-family:inherit;font-size:13px;font-weight:500;height:32px;padding:0 16px;border-radius:8px;border:1px solid %[5]s;background:%[5]s;color:#fff}
.%[1]s-btn2{cursor:pointer;font-family:inherit;font-size:13px;font-weight:500;height:32px;padding:0 12px;border-radius:8px;border:1px solid %[3]s;background:%[7]s;color:%[2]s}
.%[1]s-del{cursor:pointer;font-family:inherit;font-size:13px;font-weight:500;height:32px;padding:0 12px;border-radius:8px;border:1px solid %[6]s;background:transparent;color:%[6]s}
.%[1]s-sec{margin-top:16px;padding-top:12px;border-top:1px solid %[3]s}
.%[1]s-chips{display:flex;flex-wrap:wrap;gap:8px;margin:4px 0 8px}
.%[1]s-chip{display:inline-flex;gap:8px;align-items:center;font-size:13px;height:28px;padding:0 4px 0 12px;border-radius:999px;border:1px solid %[3]s;background:%[7]s}
.%[1]s-x{cursor:pointer;border:0;border-radius:999px;width:20px;height:20px;background:transparent;color:var(--bb-muted);font-size:14px;line-height:20px;padding:0}
.%[1]s-x:hover{color:%[6]s}
.%[1]s-search{display:flex;gap:8px}
.%[1]s-res{margin-top:8px;border:1px solid %[3]s;border-radius:8px;background:%[7]s;overflow:hidden}
.%[1]s-ri{display:flex;justify-content:space-between;align-items:center;gap:8px;padding:8px 12px;border-top:1px solid %[3]s;font-size:13px}
.%[1]s-ri:first-child{border-top:0}
.%[1]s-mut{color:var(--bb-muted);font-size:12px}
.%[1]s-ok,.%[1]s-bad{margin-bottom:12px;font-size:13px;padding:8px 12px;border-radius:8px;background:%[7]s;border:1px solid %[5]s}
.%[1]s-bad{border-color:%[6]s}
.%[1]s-bar{display:flex;gap:8px;align-items:center;margin-top:4px}
</style><div class="%[1]s-w">`, p, uiText, uiBorder, uiPanel, uiOK, uiBad, uiSunken)

	if st.Notice != "" {
		cls := "bad"
		if st.NoticeOK {
			cls = "ok"
		}
		fmt.Fprintf(&b, `<div class="%s-%s">%s</div>`, p, cls, esc(st.Notice))
	}
	fmt.Fprintf(&b, `<input type="hidden" name="row_count" value="%d"/>`, len(rows))

	for i, r := range rows {
		k := strconv.Itoa(i)
		fmt.Fprintf(&b, `<div class="%s-card"><div class="%s-grid">`, p, p)
		fmt.Fprintf(&b, `<div><label class="%[1]s-l">Repo</label><input class="%[1]s-in %[1]s-mono" name="r_repo_%[2]s" value="%[3]s" placeholder="* or repo-slug or workspace/repo"/></div>`, p, k, esc(r.Repo))
		allowSel, denySel := " selected", ""
		if r.Mode == "deny" {
			allowSel, denySel = "", " selected"
		}
		fmt.Fprintf(&b, `<div><label class="%[1]s-l">Mode</label><select class="%[1]s-in" name="r_mode_%[2]s"><option value="allow"%[3]s>allow</option><option value="deny"%[4]s>deny</option></select></div>`, p, k, allowSel, denySel)
		fmt.Fprintf(&b, `<div><label class="%[1]s-l">Min approvals</label><input class="%[1]s-in" type="number" min="0" name="r_min_%[2]s" value="%[3]s" placeholder="0"/></div>`, p, k, esc(r.Min))
		fmt.Fprintf(&b, `<div><button type="button" class="%s-del" data-op="rules_editor_delete_row" data-arg="%s">Delete</button></div>`, p, k)
		b.WriteString(`</div>`)

		// Approvers: the ids travel in a hidden input so every click posts them back.
		names := make([]string, len(r.Approvers))
		for j := range r.Approvers {
			if j < len(r.Names) {
				names[j] = r.Names[j]
			}
		}
		fmt.Fprintf(&b, `<input type="hidden" name="r_appr_%s" value="%s"/><input type="hidden" name="r_names_%s" value="%s"/>`,
			k, esc(strings.Join(r.Approvers, ",")), k, esc(strings.Join(names, "|")))
		if r.Mode == "deny" {
			fmt.Fprintf(&b, `<div class="%s-sec %s-mut">Every merge into this repo is refused. Approvers do not apply.</div></div>`, p, p)
			continue
		}
		fmt.Fprintf(&b, `<div class="%[1]s-sec"><label class="%[1]s-l">Required approvers</label><div class="%[1]s-mut">Must approve first when they are a reviewer or participant on the PR.</div><div class="%[1]s-chips">`, p)
		if len(r.Approvers) == 0 {
			fmt.Fprintf(&b, `<span class="%s-mut">none</span>`, p)
		}
		for j, id := range r.Approvers {
			label := names[j]
			if label == "" {
				label = id
			}
			fmt.Fprintf(&b, `<span class="%[1]s-chip" title="%[2]s">%[3]s <button type="button" class="%[1]s-x" title="Remove" data-op="rules_editor_remove_approver" data-arg="%[4]s|%[2]s">✕</button></span>`,
				p, esc(id), esc(label), k)
		}
		b.WriteString(`</div>`)
		q := ""
		if st.Search == i {
			q = st.Query
		}
		// No Search button: data-op-input runs the search as the operator types (debounced by the widget).
		fmt.Fprintf(&b, `<div class="%[1]s-search"><input class="%[1]s-in" name="q_%[2]s" value="%[3]s" autocomplete="off" data-op-input="rules_editor_search" data-arg="%[2]s" placeholder="Search Bitbucket user by name (results appear as you type)"/></div>`, p, k, esc(q))
		if st.Search == i && len(st.Results) > 0 {
			fmt.Fprintf(&b, `<div class="%s-res">`, p)
			for _, u := range st.Results {
				fmt.Fprintf(&b, `<div class="%[1]s-ri"><span>%[2]s <span class="%[1]s-mut">@%[3]s</span></span><button type="button" class="%[1]s-btn2" data-op="rules_editor_add_approver" data-arg="%[4]s|%[5]s|%[6]s">Add</button></div>`,
					p, esc(u.Name), esc(u.Nickname), k, esc(u.AccountID), esc(strings.ReplaceAll(u.Name, "|", " ")))
			}
			b.WriteString(`</div>`)
		}
		b.WriteString(`</div></div>`) // approvers section, card
	}

	fmt.Fprintf(&b, `<div class="%[1]s-bar"><button type="button" class="%[1]s-btn" data-op="rules_editor_save" data-arg="">Save rules</button>`+
		`<button type="button" class="%[1]s-btn2" data-op="rules_editor_add_row" data-arg="">+ Add rule</button></div>`, p)
	fmt.Fprintf(&b, `<div class="%s-mut" style="margin-top:6px">Repo: slug or workspace/slug, * matches every repo, the row with the fewest * wins. A repo no row matches merges without checks. Adding or removing an approver saves right away.</div>`, p)
	b.WriteString(`</div>`)
	return b.String()
}
