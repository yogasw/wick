package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/yogasw/wick/pkg/connector"
)

type requestParams struct {
	Method string
	URL    string
}

type fileCommitForm struct {
	Branch        string
	Path          string
	Content       string
	CommitMessage string
}

func validateSearchRepositories(c *connector.Ctx) (requestParams, error) {
	workspace, err := workspace(c)
	if err != nil {
		return requestParams{}, err
	}
	u, err := resourceURL(c, "repositories", workspace)
	if err != nil {
		return requestParams{}, err
	}
	q := make(url.Values)
	if filter := repositoryFilter(c); filter != "" {
		q.Set("q", filter)
	}
	q.Set("sort", defaultString(c.Input("sort"), "-updated_on"))
	addPage(q, c, c.InputInt("pagelen"), c.InputInt("page"))
	u = withQuery(u, q)
	return requestParams{Method: http.MethodGet, URL: u}, nil
}

func validateGetRepository(c *connector.Ctx) (requestParams, error) {
	workspace, repo, err := workspaceAndRepo(c)
	if err != nil {
		return requestParams{}, err
	}
	u, err := resourceURL(c, "repositories", workspace, repo)
	if err != nil {
		return requestParams{}, err
	}
	return requestParams{Method: http.MethodGet, URL: u}, nil
}

func validateListCommits(c *connector.Ctx) (requestParams, error) {
	workspace, repo, err := workspaceAndRepo(c)
	if err != nil {
		return requestParams{}, err
	}
	parts := []string{"repositories", workspace, repo, "commits"}
	if revision := strings.TrimSpace(c.Input("revision")); revision != "" {
		parts = append(parts, revision)
	}
	u, err := resourceURL(c, parts...)
	if err != nil {
		return requestParams{}, err
	}
	q := make(url.Values)
	if path := strings.TrimSpace(c.Input("path")); path != "" {
		q.Set("path", path)
	}
	addPage(q, c, c.InputInt("pagelen"), c.InputInt("page"))
	return requestParams{Method: http.MethodGet, URL: withQuery(u, q)}, nil
}

func validateCommit(c *connector.Ctx, kind string) (requestParams, error) {
	workspace, repo, err := workspaceAndRepo(c)
	if err != nil {
		return requestParams{}, err
	}
	commit := strings.TrimSpace(c.Input("commit"))
	if commit == "" {
		return requestParams{}, errors.New("commit is required")
	}
	var u string
	if kind == "diff" {
		u, err = resourceURL(c, "repositories", workspace, repo, "diff", commit)
	} else {
		u, err = resourceURL(c, "repositories", workspace, repo, "commit", commit)
	}
	if err != nil {
		return requestParams{}, err
	}
	return requestParams{Method: http.MethodGet, URL: u}, nil
}

func validateListPullRequests(c *connector.Ctx) (requestParams, error) {
	workspace, repo, err := workspaceAndRepo(c)
	if err != nil {
		return requestParams{}, err
	}
	u, err := resourceURL(c, "repositories", workspace, repo, "pullrequests")
	if err != nil {
		return requestParams{}, err
	}
	q := make(url.Values)
	state := defaultString(c.Input("state"), "OPEN")
	if !strings.EqualFold(state, "all") {
		q.Set("state", state)
	}
	if rawQ := strings.TrimSpace(c.Input("query")); rawQ != "" {
		q.Set("q", rawQ)
	}
	addPage(q, c, c.InputInt("pagelen"), c.InputInt("page"))
	return requestParams{Method: http.MethodGet, URL: withQuery(u, q)}, nil
}

func validatePullRequest(c *connector.Ctx, kind string) (requestParams, error) {
	workspace, repo, err := workspaceAndRepo(c)
	if err != nil {
		return requestParams{}, err
	}
	prID := c.InputInt("pull_request_id")
	if prID <= 0 {
		return requestParams{}, errors.New("pull_request_id is required")
	}
	parts := []string{"repositories", workspace, repo, "pullrequests", strconv.Itoa(prID)}
	if kind == "commits" {
		parts = append(parts, "commits")
	}
	u, err := resourceURL(c, parts...)
	if err != nil {
		return requestParams{}, err
	}
	return requestParams{Method: http.MethodGet, URL: u}, nil
}

func validateCreateBranch(c *connector.Ctx) (requestParams, map[string]any, error) {
	workspace, repo, err := workspaceAndRepo(c)
	if err != nil {
		return requestParams{}, nil, err
	}
	name := strings.TrimSpace(c.Input("name"))
	if name == "" {
		return requestParams{}, nil, errors.New("name is required")
	}
	target := strings.TrimSpace(c.Input("target"))
	if target == "" {
		return requestParams{}, nil, errors.New("target is required")
	}
	u, err := resourceURL(c, "repositories", workspace, repo, "refs", "branches")
	if err != nil {
		return requestParams{}, nil, err
	}
	body := map[string]any{
		"name": name,
		"target": map[string]any{
			"hash": target,
		},
	}
	return requestParams{Method: http.MethodPost, URL: u}, body, nil
}

func validateCreateFileCommit(c *connector.Ctx) (requestParams, fileCommitForm, error) {
	workspace, repo, err := workspaceAndRepo(c)
	if err != nil {
		return requestParams{}, fileCommitForm{}, err
	}
	form := fileCommitForm{
		Branch:        strings.TrimSpace(c.Input("branch")),
		Path:          strings.Trim(strings.TrimSpace(c.Input("path")), "/"),
		Content:       c.Input("content"),
		CommitMessage: strings.TrimSpace(c.Input("commit_message")),
	}
	if form.Branch == "" {
		return requestParams{}, fileCommitForm{}, errors.New("branch is required")
	}
	if form.Path == "" {
		return requestParams{}, fileCommitForm{}, errors.New("path is required")
	}
	if form.CommitMessage == "" {
		return requestParams{}, fileCommitForm{}, errors.New("commit_message is required")
	}
	u, err := resourceURL(c, "repositories", workspace, repo, "src")
	if err != nil {
		return requestParams{}, fileCommitForm{}, err
	}
	return requestParams{Method: http.MethodPost, URL: u}, form, nil
}

func validateCreatePullRequest(c *connector.Ctx) (requestParams, map[string]any, error) {
	workspace, repo, err := workspaceAndRepo(c)
	if err != nil {
		return requestParams{}, nil, err
	}
	title := strings.TrimSpace(c.Input("title"))
	if title == "" {
		return requestParams{}, nil, errors.New("title is required")
	}
	source := strings.TrimSpace(c.Input("source_branch"))
	if source == "" {
		return requestParams{}, nil, errors.New("source_branch is required")
	}
	destination := strings.TrimSpace(c.Input("destination_branch"))
	if destination == "" {
		return requestParams{}, nil, errors.New("destination_branch is required")
	}
	u, err := resourceURL(c, "repositories", workspace, repo, "pullrequests")
	if err != nil {
		return requestParams{}, nil, err
	}
	body := map[string]any{
		"title":       title,
		"description": c.Input("description"),
		"source": map[string]any{
			"branch": map[string]any{"name": source},
		},
		"destination": map[string]any{
			"branch": map[string]any{"name": destination},
		},
		"close_source_branch": c.InputBool("close_source_branch"),
	}
	reviewers, err := parseReviewers(c.Input("reviewers"))
	if err != nil {
		return requestParams{}, nil, err
	}
	if len(reviewers) > 0 {
		body["reviewers"] = reviewers
	}
	return requestParams{Method: http.MethodPost, URL: u}, body, nil
}

// parseReviewers turns "id1, {uuid2}" into Bitbucket reviewer objects. A value
// in braces is a user uuid; anything else is an Atlassian account_id.
// Nicknames and display names are refused: Bitbucket Cloud no longer resolves
// them, and a silent miss would leave the PR with no reviewer at all.
func parseReviewers(raw string) ([]map[string]any, error) {
	var out []map[string]any
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == '\n' || r == ' ' }) {
		v := strings.TrimSpace(part)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		switch {
		case strings.HasPrefix(v, "{") && strings.HasSuffix(v, "}"):
			out = append(out, map[string]any{"uuid": v})
		case strings.ContainsAny(v, "@{}"):
			return nil, fmt.Errorf("reviewer %q is not an account_id or {uuid}", v)
		default:
			out = append(out, map[string]any{"account_id": v})
		}
	}
	return out, nil
}

// mergeReviewers keeps the PR's current reviewers (by uuid, which every
// reviewer object returned by Bitbucket carries) and appends the new ones that
// are not already there.
func mergeReviewers(current any, add []map[string]any) []map[string]any {
	var out []map[string]any
	have := map[string]bool{}
	list, _ := current.([]any)
	for _, item := range list {
		r, _ := item.(map[string]any)
		if r == nil {
			continue
		}
		if id, _ := r["account_id"].(string); id != "" {
			have[id] = true
		}
		if uuid, _ := r["uuid"].(string); uuid != "" {
			have[uuid] = true
			out = append(out, map[string]any{"uuid": uuid})
		} else if id, _ := r["account_id"].(string); id != "" {
			out = append(out, map[string]any{"account_id": id})
		}
	}
	for _, r := range add {
		key, _ := r["account_id"].(string)
		if key == "" {
			key, _ = r["uuid"].(string)
		}
		if have[key] {
			continue
		}
		have[key] = true
		out = append(out, r)
	}
	return out
}

func validateCreatePullRequestComment(c *connector.Ctx) (requestParams, map[string]any, error) {
	workspace, repo, err := workspaceAndRepo(c)
	if err != nil {
		return requestParams{}, nil, err
	}
	prID := c.InputInt("pull_request_id")
	if prID <= 0 {
		return requestParams{}, nil, errors.New("pull_request_id is required")
	}
	body := strings.TrimSpace(c.Input("body"))
	if body == "" {
		return requestParams{}, nil, errors.New("body is required")
	}
	u, err := resourceURL(c, "repositories", workspace, repo, "pullrequests", strconv.Itoa(prID), "comments")
	if err != nil {
		return requestParams{}, nil, err
	}
	payload := map[string]any{
		"content": map[string]any{"raw": body},
	}
	inlinePath := strings.TrimSpace(c.Input("inline_path"))
	inlineTo := c.InputInt("inline_to")
	inlineFrom := c.InputInt("inline_from")
	if inlinePath == "" && (inlineTo > 0 || inlineFrom > 0) {
		return requestParams{}, nil, errors.New("inline_path is required when inline_to or inline_from is set")
	}
	if inlinePath != "" {
		inline := map[string]any{"path": inlinePath}
		if inlineTo > 0 {
			inline["to"] = inlineTo
		} else if inlineFrom > 0 {
			inline["from"] = inlineFrom
		}
		payload["inline"] = inline
	}
	return requestParams{Method: http.MethodPost, URL: u}, payload, nil
}

func validatePullRequestAction(c *connector.Ctx, action string) (requestParams, error) {
	workspace, repo, err := workspaceAndRepo(c)
	if err != nil {
		return requestParams{}, err
	}
	prID := c.InputInt("pull_request_id")
	if prID <= 0 {
		return requestParams{}, errors.New("pull_request_id is required")
	}
	u, err := resourceURL(c, "repositories", workspace, repo, "pullrequests", strconv.Itoa(prID), action)
	if err != nil {
		return requestParams{}, err
	}
	return requestParams{Method: http.MethodPost, URL: u}, nil
}

func validateMergePullRequest(c *connector.Ctx) (requestParams, map[string]any, error) {
	workspace, repo, err := workspaceAndRepo(c)
	if err != nil {
		return requestParams{}, nil, err
	}
	prID := c.InputInt("pull_request_id")
	if prID <= 0 {
		return requestParams{}, nil, errors.New("pull_request_id is required")
	}
	u, err := resourceURL(c, "repositories", workspace, repo, "pullrequests", strconv.Itoa(prID), "merge")
	if err != nil {
		return requestParams{}, nil, err
	}
	body := map[string]any{
		"close_source_branch": c.InputBool("close_source_branch"),
	}
	if strategy := strings.TrimSpace(c.Input("merge_strategy")); strategy != "" {
		body["merge_strategy"] = strategy
	}
	if msg := strings.TrimSpace(c.Input("message")); msg != "" {
		body["message"] = msg
	}
	return requestParams{Method: http.MethodPost, URL: u}, body, nil
}

func workspaceAndRepo(c *connector.Ctx) (string, string, error) {
	workspace, err := workspace(c)
	if err != nil {
		return "", "", err
	}
	repo := strings.TrimSpace(c.Input("repo_slug"))
	if repo == "" {
		return "", "", errors.New("repo_slug is required")
	}
	return workspace, repo, nil
}

func workspace(c *connector.Ctx) (string, error) {
	workspace := strings.TrimSpace(c.Input("workspace"))
	if workspace == "" {
		workspace = strings.TrimSpace(c.Cfg("default_workspace"))
	}
	if workspace == "" {
		return "", errors.New("workspace is required when default_workspace is not configured")
	}
	return workspace, nil
}

func resourceURL(c *connector.Ctx, parts ...string) (string, error) {
	base := strings.TrimRight(strings.TrimSpace(c.Cfg("base_url")), "/")
	if base == "" {
		return "", errors.New("base_url is not configured")
	}
	escaped := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.Trim(part, "/")
		if part == "" {
			continue
		}
		escaped = append(escaped, url.PathEscape(part))
	}
	return base + "/" + strings.Join(escaped, "/"), nil
}

func repositoryFilter(c *connector.Ctx) string {
	clauses := make([]string, 0, 3)
	query := strings.TrimSpace(c.Input("query"))
	if query != "" {
		v := quoteQuery(query)
		clauses = append(clauses, fmt.Sprintf("(name~%s OR slug~%s OR description~%s)", v, v, v))
	}
	if project := strings.TrimSpace(c.Input("project_key")); project != "" {
		clauses = append(clauses, "project.key="+quoteQuery(project))
	}
	switch strings.ToLower(strings.TrimSpace(c.Input("is_private"))) {
	case "true":
		clauses = append(clauses, "is_private=true")
	case "false":
		clauses = append(clauses, "is_private=false")
	}
	return strings.Join(clauses, " AND ")
}

func quoteQuery(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func addPage(q url.Values, c *connector.Ctx, requestedPagelen, requestedPage int) {
	pagelen := requestedPagelen
	if pagelen <= 0 {
		pagelen = c.CfgInt("default_pagelen")
	}
	if pagelen <= 0 {
		pagelen = 20
	}
	max := c.CfgInt("max_pagelen")
	if max <= 0 {
		max = 100
	}
	if pagelen > max {
		pagelen = max
	}
	q.Set("pagelen", strconv.Itoa(pagelen))
	page := requestedPage
	if page <= 0 {
		page = 1
	}
	q.Set("page", strconv.Itoa(page))
}

func withQuery(raw string, q url.Values) string {
	if len(q) == 0 {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	existing := u.Query()
	for k, values := range q {
		for _, v := range values {
			existing.Add(k, v)
		}
	}
	u.RawQuery = existing.Encode()
	return u.String()
}

func defaultString(v, fallback string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	return v
}

// parsePipelineVariables accepts a JSON object or KEY=VALUE lines.
func parsePipelineVariables(raw string) ([]map[string]any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	pairs := map[string]string{}
	var order []string
	if strings.HasPrefix(raw, "{") {
		var m map[string]any
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return nil, fmt.Errorf("variables is not valid JSON: %w", err)
		}
		for k, v := range m {
			pairs[k] = fmt.Sprint(v)
			order = append(order, k)
		}
	} else {
		for _, line := range strings.Split(raw, "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			k = strings.TrimSpace(k)
			if !ok || k == "" {
				return nil, fmt.Errorf("variables line %q must be KEY=VALUE", line)
			}
			pairs[k] = v
			order = append(order, k)
		}
	}
	out := make([]map[string]any, 0, len(order))
	for _, k := range order {
		out = append(out, map[string]any{"key": k, "value": pairs[k]})
	}
	return out, nil
}

// pipelineSelectorType maps the selector_type input to the selector type
// Bitbucket expects. Empty keeps the original behaviour (custom).
func pipelineSelectorType(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "custom":
		return "custom", nil
	case "branches", "branch":
		return "branches", nil
	case "pull-requests", "pull-request", "pr":
		return "pull-requests", nil
	}
	return "", errors.New("selector_type must be custom, branches or pull-requests")
}

func validateRunPipeline(c *connector.Ctx) (requestParams, map[string]any, error) {
	workspace, repo, err := workspaceAndRepo(c)
	if err != nil {
		return requestParams{}, nil, err
	}
	branch := strings.TrimSpace(c.Input("branch"))
	if branch == "" {
		return requestParams{}, nil, errors.New("branch is required")
	}
	vars, err := parsePipelineVariables(c.Input("variables"))
	if err != nil {
		return requestParams{}, nil, err
	}
	u, err := resourceURL(c, "repositories", workspace, repo, "pipelines")
	if err != nil {
		return requestParams{}, nil, err
	}
	target := map[string]any{
		"type":     "pipeline_ref_target",
		"ref_type": "branch",
		"ref_name": branch,
	}
	if pattern := strings.TrimSpace(c.Input("pattern")); pattern != "" {
		selType, err := pipelineSelectorType(c.Input("selector_type"))
		if err != nil {
			return requestParams{}, nil, err
		}
		target["selector"] = map[string]any{"type": selType, "pattern": pattern}
	}
	body := map[string]any{"target": target}
	if len(vars) > 0 {
		body["variables"] = vars
	}
	// Bitbucket expects a trailing slash on the pipelines collection.
	return requestParams{Method: http.MethodPost, URL: u + "/"}, body, nil
}

func validateGetPipeline(c *connector.Ctx) (requestParams, error) {
	workspace, repo, err := workspaceAndRepo(c)
	if err != nil {
		return requestParams{}, err
	}
	id := strings.TrimSpace(c.Input("pipeline_uuid"))
	if id == "" {
		return requestParams{}, errors.New("pipeline_uuid is required")
	}
	u, err := resourceURL(c, "repositories", workspace, repo, "pipelines", id)
	if err != nil {
		return requestParams{}, err
	}
	return requestParams{Method: http.MethodGet, URL: u}, nil
}

func validateListPipelines(c *connector.Ctx) (requestParams, error) {
	workspace, repo, err := workspaceAndRepo(c)
	if err != nil {
		return requestParams{}, err
	}
	u, err := resourceURL(c, "repositories", workspace, repo, "pipelines")
	if err != nil {
		return requestParams{}, err
	}
	q := make(url.Values)
	q.Set("sort", "-created_on")
	addPage(q, c, c.InputInt("pagelen"), c.InputInt("page"))
	return requestParams{Method: http.MethodGet, URL: withQuery(u+"/", q)}, nil
}
