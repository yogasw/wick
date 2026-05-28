package bitbucket

import (
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
	return requestParams{Method: http.MethodPost, URL: u}, body, nil
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
	return requestParams{Method: http.MethodPost, URL: u}, payload, nil
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
