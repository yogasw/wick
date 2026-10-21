package main

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

// ── helpers ──────────────────────────────────────────────────────────

// projectPath returns the URL-escaped project id: a numeric id or a
// group/sub/repo path ("/" becomes %2F as GitLab requires).
func projectPath(c *connector.Ctx) (string, error) {
	p := strings.Trim(strings.TrimSpace(c.Input("project")), "/")
	if p == "" {
		p = strings.Trim(strings.TrimSpace(c.Cfg("default_project")), "/")
	}
	if p == "" {
		return "", fmt.Errorf("project is required (numeric id or group/repo path)")
	}
	return "/projects/" + url.PathEscape(p), nil
}

func requireInt(c *connector.Ctx, key string) (int, error) {
	v := c.InputInt(key)
	if v <= 0 {
		return 0, fmt.Errorf("%s is required", key)
	}
	return v, nil
}

func perPage(c *connector.Ctx) string {
	n := c.InputInt("per_page")
	if n <= 0 {
		n = 20
	}
	if n > 100 {
		n = 100
	}
	return strconv.Itoa(n)
}

// query builds url.Values from the given input keys that are set, with
// optional renames ("input_key=api_key").
func query(c *connector.Ctx, keys ...string) url.Values {
	q := url.Values{}
	for _, k := range keys {
		in, out, ok := strings.Cut(k, "=")
		if !ok {
			out = in
		}
		if v := strings.TrimSpace(c.Input(in)); v != "" {
			q.Set(out, v)
		}
	}
	return q
}

func setIf(m map[string]any, key, val string) {
	if strings.TrimSpace(val) != "" {
		m[key] = val
	}
}

// pick keeps only keys from a decoded object (or each object of a list).
func pick(v any, keys ...string) any {
	switch t := v.(type) {
	case []any:
		out := make([]any, 0, len(t))
		for _, e := range t {
			out = append(out, pick(e, keys...))
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for _, k := range keys {
			if inner, sub, ok := strings.Cut(k, "."); ok {
				if m, ok := t[inner].(map[string]any); ok {
					out[inner+"_"+sub] = m[sub]
				}
				continue
			}
			if val, ok := t[k]; ok {
				out[k] = val
			}
		}
		return out
	}
	return v
}

var (
	projectKeys  = []string{"id", "path_with_namespace", "name", "description", "default_branch", "visibility", "web_url", "last_activity_at", "archived"}
	mrKeys       = []string{"iid", "title", "state", "draft", "source_branch", "target_branch", "author.username", "merge_status", "detailed_merge_status", "has_conflicts", "web_url", "created_at", "updated_at", "merged_at", "merged_by.username", "merge_commit_sha", "sha"}
	pipelineKeys = []string{"id", "iid", "status", "ref", "sha", "source", "web_url", "created_at", "updated_at"}
	jobKeys      = []string{"id", "name", "stage", "status", "duration", "web_url", "failure_reason", "allow_failure"}
	commitKeys   = []string{"id", "short_id", "title", "author_name", "author_email", "created_at", "web_url"}
	issueKeys    = []string{"iid", "title", "state", "labels", "author.username", "assignee.username", "web_url", "created_at", "updated_at"}
	noteKeys     = []string{"id", "body", "author.username", "system", "created_at", "resolvable", "resolved"}
)

func get(c *connector.Ctx, path string, q url.Values, keys []string) (any, error) {
	v, err := doJSON(c, http.MethodGet, apiURL(c, path, q), nil)
	if err != nil {
		return nil, err
	}
	if keys == nil {
		return v, nil
	}
	return pick(v, keys...), nil
}

func tail(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	return "…(truncated)\n" + s[len(s)-max:]
}

// ── user / projects ──────────────────────────────────────────────────

func getMe(c *connector.Ctx) (any, error) {
	return get(c, "/user", nil, []string{"id", "username", "name", "email", "state", "web_url", "is_admin"})
}

func listProjects(c *connector.Ctx) (any, error) {
	q := query(c, "search", "order_by")
	q.Set("per_page", perPage(c))
	if q.Get("order_by") == "" {
		q.Set("order_by", "last_activity_at")
	}
	if !c.InputBool("all") {
		q.Set("membership", "true")
	}
	return get(c, "/projects", q, projectKeys)
}

func getProject(c *connector.Ctx) (any, error) {
	p, err := projectPath(c)
	if err != nil {
		return nil, err
	}
	return get(c, p, nil, projectKeys)
}

// ── repository ───────────────────────────────────────────────────────

func listBranches(c *connector.Ctx) (any, error) {
	p, err := projectPath(c)
	if err != nil {
		return nil, err
	}
	q := query(c, "search")
	q.Set("per_page", perPage(c))
	return get(c, p+"/repository/branches", q, []string{"name", "merged", "protected", "default", "commit.id", "commit.title", "commit.committed_date", "web_url"})
}

func createBranch(c *connector.Ctx) (any, error) {
	p, err := projectPath(c)
	if err != nil {
		return nil, err
	}
	q := query(c, "branch", "ref")
	if q.Get("branch") == "" || q.Get("ref") == "" {
		return nil, fmt.Errorf("branch and ref are required")
	}
	v, err := doJSON(c, http.MethodPost, apiURL(c, p+"/repository/branches", q), nil)
	if err != nil {
		return nil, err
	}
	return pick(v, "name", "commit.id", "web_url"), nil
}

func listCommits(c *connector.Ctx) (any, error) {
	p, err := projectPath(c)
	if err != nil {
		return nil, err
	}
	q := query(c, "ref=ref_name", "path", "since", "until", "author")
	q.Set("per_page", perPage(c))
	return get(c, p+"/repository/commits", q, commitKeys)
}

func getCommit(c *connector.Ctx) (any, error) {
	p, err := projectPath(c)
	if err != nil {
		return nil, err
	}
	sha := strings.TrimSpace(c.Input("sha"))
	if sha == "" {
		return nil, fmt.Errorf("sha is required")
	}
	return get(c, p+"/repository/commits/"+url.PathEscape(sha), nil, append(commitKeys, "message", "parent_ids", "stats"))
}

func compareRefs(c *connector.Ctx) (any, error) {
	p, err := projectPath(c)
	if err != nil {
		return nil, err
	}
	q := query(c, "from", "to")
	if q.Get("from") == "" || q.Get("to") == "" {
		return nil, fmt.Errorf("from and to are required")
	}
	v, err := get(c, p+"/repository/compare", q, nil)
	if err != nil {
		return nil, err
	}
	m, _ := v.(map[string]any)
	out := map[string]any{"commits": pick(m["commits"], commitKeys...), "web_url": m["web_url"]}
	out["diffs"] = shapeDiffs(m["diffs"], c.InputInt("max_bytes"))
	return out, nil
}

func getFile(c *connector.Ctx) (any, error) {
	p, err := projectPath(c)
	if err != nil {
		return nil, err
	}
	path := strings.Trim(strings.TrimSpace(c.Input("path")), "/")
	if path == "" {
		return nil, fmt.Errorf("path is required")
	}
	ref := strings.TrimSpace(c.Input("ref"))
	if ref == "" {
		ref = "HEAD"
	}
	raw, _, err := doRaw(c, http.MethodGet, apiURL(c, p+"/repository/files/"+url.PathEscape(path)+"/raw", url.Values{"ref": {ref}}), nil)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": path, "ref": ref, "size": len(raw), "content": tail(string(raw), c.InputInt("max_bytes"))}, nil
}

func listTree(c *connector.Ctx) (any, error) {
	p, err := projectPath(c)
	if err != nil {
		return nil, err
	}
	q := query(c, "path", "ref")
	q.Set("per_page", perPage(c))
	if c.InputBool("recursive") {
		q.Set("recursive", "true")
	}
	return get(c, p+"/repository/tree", q, []string{"name", "path", "type"})
}

func listTags(c *connector.Ctx) (any, error) {
	p, err := projectPath(c)
	if err != nil {
		return nil, err
	}
	q := query(c, "search")
	q.Set("per_page", perPage(c))
	return get(c, p+"/repository/tags", q, []string{"name", "message", "target", "commit.id", "commit.created_at"})
}

// ── merge requests ───────────────────────────────────────────────────

func mrPath(c *connector.Ctx) (string, error) {
	p, err := projectPath(c)
	if err != nil {
		return "", err
	}
	iid, err := requireInt(c, "mr_iid")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/merge_requests/%d", p, iid), nil
}

func listMRs(c *connector.Ctx) (any, error) {
	p, err := projectPath(c)
	if err != nil {
		return nil, err
	}
	q := query(c, "state", "target_branch", "source_branch", "author=author_username", "search")
	q.Set("per_page", perPage(c))
	if q.Get("state") == "" {
		q.Set("state", "opened")
	}
	return get(c, p+"/merge_requests", q, mrKeys)
}

func getMR(c *connector.Ctx) (any, error) {
	mp, err := mrPath(c)
	if err != nil {
		return nil, err
	}
	return get(c, mp, nil, append(mrKeys, "description", "head_pipeline", "reviewers", "assignees", "user_notes_count", "changes_count"))
}

func shapeDiffs(v any, maxBytes int) any {
	list, _ := v.([]any)
	out := make([]any, 0, len(list))
	budget := maxBytes
	for _, e := range list {
		d, _ := e.(map[string]any)
		diff, _ := d["diff"].(string)
		if maxBytes > 0 {
			if budget <= 0 {
				diff = "(omitted: max_bytes reached)"
			} else if len(diff) > budget {
				diff = diff[:budget] + "\n…(truncated)"
			}
			budget -= len(diff)
		}
		out = append(out, map[string]any{
			"old_path": d["old_path"], "new_path": d["new_path"], "new_file": d["new_file"],
			"deleted_file": d["deleted_file"], "renamed_file": d["renamed_file"], "diff": diff,
		})
	}
	return out
}

func getMRChanges(c *connector.Ctx) (any, error) {
	mp, err := mrPath(c)
	if err != nil {
		return nil, err
	}
	v, err := get(c, mp+"/diffs", url.Values{"per_page": {"100"}}, nil)
	if ae, ok := err.(*apiError); ok && ae.Status == http.StatusNotFound {
		// GitLab older than 15.7 has no /diffs; /changes wraps the list.
		var whole any
		if whole, err = get(c, mp+"/changes", nil, nil); err == nil {
			m, _ := whole.(map[string]any)
			v = m["changes"]
		}
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"files": shapeDiffs(v, c.InputInt("max_bytes"))}, nil
}

func createMR(c *connector.Ctx) (any, error) {
	p, err := projectPath(c)
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	for _, k := range []string{"source_branch", "target_branch", "title"} {
		if strings.TrimSpace(c.Input(k)) == "" {
			return nil, fmt.Errorf("%s is required", k)
		}
		body[k] = c.Input(k)
	}
	setIf(body, "description", c.Input("description"))
	if c.InputBool("draft") && !strings.HasPrefix(strings.ToLower(c.Input("title")), "draft:") {
		body["title"] = "Draft: " + c.Input("title")
	}
	if c.InputBool("remove_source_branch") {
		body["remove_source_branch"] = true
	}
	if c.InputBool("squash") {
		body["squash"] = true
	}
	v, err := doJSON(c, http.MethodPost, apiURL(c, p+"/merge_requests", nil), body)
	if err != nil {
		return nil, err
	}
	return pick(v, mrKeys...), nil
}

func updateMR(c *connector.Ctx) (any, error) {
	mp, err := mrPath(c)
	if err != nil {
		return nil, err
	}
	body := map[string]any{}
	setIf(body, "title", c.Input("title"))
	setIf(body, "description", c.Input("description"))
	setIf(body, "target_branch", c.Input("target_branch"))
	setIf(body, "state_event", c.Input("state_event"))
	if len(body) == 0 {
		return nil, fmt.Errorf("nothing to update: pass title, description, target_branch or state_event")
	}
	v, err := doJSON(c, http.MethodPut, apiURL(c, mp, nil), body)
	if err != nil {
		return nil, err
	}
	return pick(v, mrKeys...), nil
}

// mergeMR checks merge_rules before merging, from the MR as it is right now,
// so an approval withdrawn a minute ago counts as withdrawn.
func mergeMR(c *connector.Ctx) (any, error) {
	mp, err := mrPath(c)
	if err != nil {
		return nil, err
	}
	project := strings.Trim(strings.TrimSpace(c.Input("project")), "/")
	if project == "" {
		project = strings.Trim(strings.TrimSpace(c.Cfg("default_project")), "/")
	}
	verdict, _, err := checkMergeRules(c, project, c.InputInt("mr_iid"))
	if err != nil {
		return nil, err
	}
	if !verdict.Allowed {
		return nil, errMergeRefused(verdict)
	}
	body := map[string]any{}
	if c.InputBool("squash") {
		body["squash"] = true
	}
	if c.InputBool("remove_source_branch") {
		body["should_remove_source_branch"] = true
	}
	if c.InputBool("when_pipeline_succeeds") {
		body["merge_when_pipeline_succeeds"] = true
	}
	setIf(body, "merge_commit_message", c.Input("merge_commit_message"))
	setIf(body, "sha", c.Input("sha"))
	v, err := doJSON(c, http.MethodPut, apiURL(c, mp+"/merge", nil), body)
	if err != nil {
		return nil, err
	}
	return pick(v, mrKeys...), nil
}

func approveMR(c *connector.Ctx) (any, error) {
	mp, err := mrPath(c)
	if err != nil {
		return nil, err
	}
	v, err := doJSON(c, http.MethodPost, apiURL(c, mp+"/approve", nil), nil)
	if err != nil {
		return nil, err
	}
	return pick(v, "approved", "approvals_left", "approved_by"), nil
}

func listMRNotes(c *connector.Ctx) (any, error) {
	mp, err := mrPath(c)
	if err != nil {
		return nil, err
	}
	return get(c, mp+"/notes", url.Values{"sort": {"asc"}, "per_page": {perPage(c)}}, noteKeys)
}

func addMRNote(c *connector.Ctx) (any, error) {
	mp, err := mrPath(c)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.Input("body")) == "" {
		return nil, fmt.Errorf("body is required")
	}
	v, err := doJSON(c, http.MethodPost, apiURL(c, mp+"/notes", nil), map[string]any{"body": c.Input("body")})
	if err != nil {
		return nil, err
	}
	return pick(v, noteKeys...), nil
}

func listMRPipelines(c *connector.Ctx) (any, error) {
	mp, err := mrPath(c)
	if err != nil {
		return nil, err
	}
	return get(c, mp+"/pipelines", nil, pipelineKeys)
}

// ── pipelines / jobs ─────────────────────────────────────────────────

func listPipelines(c *connector.Ctx) (any, error) {
	p, err := projectPath(c)
	if err != nil {
		return nil, err
	}
	q := query(c, "ref", "status", "sha")
	q.Set("per_page", perPage(c))
	return get(c, p+"/pipelines", q, pipelineKeys)
}

func getPipeline(c *connector.Ctx) (any, error) {
	p, err := projectPath(c)
	if err != nil {
		return nil, err
	}
	id, err := requireInt(c, "pipeline_id")
	if err != nil {
		return nil, err
	}
	return get(c, fmt.Sprintf("%s/pipelines/%d", p, id), nil, append(pipelineKeys, "duration", "finished_at", "user.username", "detailed_status"))
}

func listPipelineJobs(c *connector.Ctx) (any, error) {
	p, err := projectPath(c)
	if err != nil {
		return nil, err
	}
	id, err := requireInt(c, "pipeline_id")
	if err != nil {
		return nil, err
	}
	q := query(c, "scope")
	q.Set("per_page", "100")
	return get(c, fmt.Sprintf("%s/pipelines/%d/jobs", p, id), q, jobKeys)
}

func getJobLog(c *connector.Ctx) (any, error) {
	p, err := projectPath(c)
	if err != nil {
		return nil, err
	}
	id, err := requireInt(c, "job_id")
	if err != nil {
		return nil, err
	}
	raw, _, err := doRaw(c, http.MethodGet, apiURL(c, fmt.Sprintf("%s/jobs/%d/trace", p, id), nil), nil)
	if err != nil {
		return nil, err
	}
	max := c.InputInt("max_bytes")
	if max == 0 {
		max = 20000
	}
	return map[string]any{"job_id": id, "bytes": len(raw), "log": tail(string(raw), max)}, nil
}

func pipelineAction(action string) connector.ExecuteFunc {
	return func(c *connector.Ctx) (any, error) {
		p, err := projectPath(c)
		if err != nil {
			return nil, err
		}
		id, err := requireInt(c, "pipeline_id")
		if err != nil {
			return nil, err
		}
		v, err := doJSON(c, http.MethodPost, apiURL(c, fmt.Sprintf("%s/pipelines/%d/%s", p, id, action), nil), nil)
		if err != nil {
			return nil, err
		}
		return pick(v, pipelineKeys...), nil
	}
}

// ── issues ───────────────────────────────────────────────────────────

func listIssues(c *connector.Ctx) (any, error) {
	p, err := projectPath(c)
	if err != nil {
		return nil, err
	}
	q := query(c, "state", "labels", "search", "assignee=assignee_username")
	q.Set("per_page", perPage(c))
	if q.Get("state") == "" {
		q.Set("state", "opened")
	}
	return get(c, p+"/issues", q, issueKeys)
}

func getIssue(c *connector.Ctx) (any, error) {
	p, err := projectPath(c)
	if err != nil {
		return nil, err
	}
	iid, err := requireInt(c, "issue_iid")
	if err != nil {
		return nil, err
	}
	return get(c, fmt.Sprintf("%s/issues/%d", p, iid), nil, append(issueKeys, "description", "milestone", "due_date"))
}

func createIssue(c *connector.Ctx) (any, error) {
	p, err := projectPath(c)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.Input("title")) == "" {
		return nil, fmt.Errorf("title is required")
	}
	body := map[string]any{"title": c.Input("title")}
	setIf(body, "description", c.Input("description"))
	setIf(body, "labels", c.Input("labels"))
	v, err := doJSON(c, http.MethodPost, apiURL(c, p+"/issues", nil), body)
	if err != nil {
		return nil, err
	}
	return pick(v, issueKeys...), nil
}

func addIssueNote(c *connector.Ctx) (any, error) {
	p, err := projectPath(c)
	if err != nil {
		return nil, err
	}
	iid, err := requireInt(c, "issue_iid")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.Input("body")) == "" {
		return nil, fmt.Errorf("body is required")
	}
	v, err := doJSON(c, http.MethodPost, apiURL(c, fmt.Sprintf("%s/issues/%d/notes", p, iid), nil), map[string]any{"body": c.Input("body")})
	if err != nil {
		return nil, err
	}
	return pick(v, noteKeys...), nil
}
