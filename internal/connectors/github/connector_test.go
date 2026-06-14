package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/yogasw/wick/pkg/connector"
)

func newCtx(configs, input map[string]string) *connector.Ctx {
	return connector.NewCtx(context.Background(), "test", configs, input, http.DefaultClient, nil, nil)
}

func mockSrv(t *testing.T, status int, body any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if body != nil {
			_ = json.NewEncoder(w).Encode(body)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestListRepos(t *testing.T) {
	srv := mockSrv(t, 200, []map[string]any{{"name": "myrepo", "visibility": "public"}})
	c := newCtx(
		map[string]string{"base_url": srv.URL, "token": "ghp_test"},
		map[string]string{},
	)
	result, err := listRepos(c)
	require.NoError(t, err)
	arr, ok := result.([]any)
	require.True(t, ok)
	assert.Len(t, arr, 1)
}

func TestListIssues(t *testing.T) {
	srv := mockSrv(t, 200, []map[string]any{{"number": 1, "title": "Bug report", "state": "open"}})
	c := newCtx(
		map[string]string{"base_url": srv.URL, "token": "ghp_test"},
		map[string]string{"owner": "octocat", "repo": "hello-world"},
	)
	result, err := listIssues(c)
	require.NoError(t, err)
	assert.NotNil(t, result)
}

func TestCreateIssue(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"number": 42, "html_url": "https://github.com/octocat/hello/issues/42"})
	}))
	t.Cleanup(srv.Close)

	c := newCtx(
		map[string]string{"base_url": srv.URL, "token": "ghp_test"},
		map[string]string{"owner": "octocat", "repo": "hello", "title": "New bug", "body": "details", "labels": "bug,help wanted"},
	)
	result, err := createIssue(c)
	require.NoError(t, err)
	assert.Equal(t, "New bug", captured["title"])
	assert.ElementsMatch(t, []any{"bug", "help wanted"}, captured["labels"])
	m := result.(map[string]any)
	assert.Equal(t, float64(42), m["number"])
}

func TestGetFileDecodesBase64(t *testing.T) {
	content := "package main\n\nfunc main() {}\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(content))
	srv := mockSrv(t, 200, map[string]any{
		"name":     "main.go",
		"content":  encoded + "\n",
		"encoding": "base64",
	})
	c := newCtx(
		map[string]string{"base_url": srv.URL, "token": "ghp_test"},
		map[string]string{"owner": "octocat", "repo": "hello", "path": "main.go"},
	)
	result, err := getFile(c)
	require.NoError(t, err)
	m := result.(map[string]any)
	assert.Equal(t, content, m["content"])
}

func TestMissingToken(t *testing.T) {
	srv := mockSrv(t, 200, []any{})
	c := newCtx(
		map[string]string{"base_url": srv.URL},
		map[string]string{},
	)
	_, err := listRepos(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token")
}

func TestMissingOwnerRepo(t *testing.T) {
	c := newCtx(
		map[string]string{"token": "ghp_test"},
		map[string]string{"owner": "", "repo": ""},
	)
	_, err := listIssues(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "owner")
}

func TestGitHubAPIError(t *testing.T) {
	srv := mockSrv(t, 401, map[string]any{"message": "Bad credentials"})
	c := newCtx(
		map[string]string{"base_url": srv.URL, "token": "bad"},
		map[string]string{},
	)
	_, err := listRepos(c)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Bad credentials")
}

func TestParseCSV(t *testing.T) {
	assert.Equal(t, []string{"bug", "help wanted"}, parseCSV("bug,help wanted"))
	assert.Equal(t, []string{"solo"}, parseCSV("  solo  "))
	assert.Nil(t, parseCSV(""))
}

func TestMeta(t *testing.T) {
	m := Meta()
	assert.Equal(t, Key, m.Key)
	assert.NotEmpty(t, m.Name)
	assert.NotEmpty(t, m.Description)
}

func TestOperations(t *testing.T) {
	ops := Operations()
	assert.Len(t, ops, 57)
	keys := make([]string, len(ops))
	for i, op := range ops {
		keys[i] = op.Key
	}
	assert.ElementsMatch(t, []string{
		"list_repos", "list_issues", "create_issue", "get_file", "list_prs", "add_comment",
		"get_pr_diff", "merge_pr", "create_pr", "create_or_update_file",
		"get_repo", "list_branches", "list_commits", "list_forks", "create_fork",
		"list_stargazers", "star_repo", "unstar_repo",
		"get_issue", "update_issue", "list_issue_comments",
		"get_pr", "list_pr_files", "update_pr",
		"list_releases", "get_latest_release", "get_release", "create_release", "update_release", "delete_release",
		"list_tags", "get_me",
		"update_comment", "delete_comment",
		"create_review", "list_reviews", "create_review_comment", "list_review_comments", "request_reviewers",
		"create_branch", "delete_ref",
		"add_labels", "remove_label", "add_assignees",
		"get_commit", "compare_commits",
		"search_issues", "search_repos", "search_code",
		"list_collaborators", "create_repo", "update_repo",
		"list_workflows", "list_workflow_runs", "dispatch_workflow",
		"list_hooks", "create_hook",
	}, keys)
}

func TestGetPRDiff(t *testing.T) {
	diff := "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new\n"
	var gotMethod, gotPath, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotAccept = r.Method, r.URL.Path, r.Header.Get("Accept")
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(diff))
	}))
	t.Cleanup(srv.Close)

	c := newCtx(
		map[string]string{"base_url": srv.URL, "token": "ghp_test"},
		map[string]string{"owner": "octocat", "repo": "hello", "number": "7"},
	)
	result, err := getPRDiff(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", gotMethod)
	assert.Equal(t, "/repos/octocat/hello/pulls/7", gotPath)
	assert.Equal(t, "application/vnd.github.v3.diff", gotAccept)

	m := result.(map[string]any)
	assert.Equal(t, diff, m["diff"])
	assert.Equal(t, false, m["truncated"])
	assert.Equal(t, len(diff), m["bytes"])
}

func TestGetPRDiffTruncates(t *testing.T) {
	diff := "diff --git a/main.go b/main.go\n--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(diff))
	}))
	t.Cleanup(srv.Close)

	c := newCtx(
		map[string]string{"base_url": srv.URL, "token": "ghp_test"},
		map[string]string{"owner": "octocat", "repo": "hello", "number": "7", "max_bytes": "10"},
	)
	result, err := getPRDiff(c)
	require.NoError(t, err)
	m := result.(map[string]any)
	assert.Equal(t, true, m["truncated"])
	assert.Equal(t, diff[:10]+"\n…[diff truncated]", m["diff"])
}

func TestMergePR(t *testing.T) {
	var gotMethod, gotPath string
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]any{"merged": true, "sha": "abc123", "message": "Pull Request successfully merged"})
	}))
	t.Cleanup(srv.Close)

	c := newCtx(
		map[string]string{"base_url": srv.URL, "token": "ghp_test"},
		map[string]string{"owner": "octocat", "repo": "hello", "number": "7", "merge_method": "squash", "commit_title": "Ship it"},
	)
	result, err := mergePR(c)
	require.NoError(t, err)
	assert.Equal(t, "PUT", gotMethod)
	assert.Equal(t, "/repos/octocat/hello/pulls/7/merge", gotPath)
	assert.Equal(t, "squash", captured["merge_method"])
	assert.Equal(t, "Ship it", captured["commit_title"])
	m := result.(map[string]any)
	assert.Equal(t, true, m["merged"])
	assert.Equal(t, "abc123", m["sha"])
}

func TestMergePRDefaultMethod(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.WriteHeader(200)
		_ = json.NewEncoder(w).Encode(map[string]any{"merged": true})
	}))
	t.Cleanup(srv.Close)

	c := newCtx(
		map[string]string{"base_url": srv.URL, "token": "ghp_test"},
		map[string]string{"owner": "octocat", "repo": "hello", "number": "7"},
	)
	_, err := mergePR(c)
	require.NoError(t, err)
	assert.Equal(t, "merge", captured["merge_method"])
	_, hasTitle := captured["commit_title"]
	assert.False(t, hasTitle)
}

func TestCreatePR(t *testing.T) {
	var gotMethod, gotPath string
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&captured)
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"number": 51, "html_url": "https://github.com/octocat/hello/pull/51", "state": "open"})
	}))
	t.Cleanup(srv.Close)

	c := newCtx(
		map[string]string{"base_url": srv.URL, "token": "ghp_test"},
		map[string]string{"owner": "octocat", "repo": "hello", "title": "Add retry", "head": "feature/retry", "base": "main", "body": "Closes #42", "draft": "true"},
	)
	result, err := createPR(c)
	require.NoError(t, err)
	assert.Equal(t, "POST", gotMethod)
	assert.Equal(t, "/repos/octocat/hello/pulls", gotPath)
	assert.Equal(t, "Add retry", captured["title"])
	assert.Equal(t, "feature/retry", captured["head"])
	assert.Equal(t, "main", captured["base"])
	assert.Equal(t, "Closes #42", captured["body"])
	assert.Equal(t, true, captured["draft"])
	m := result.(map[string]any)
	assert.Equal(t, float64(51), m["number"])
	assert.Equal(t, "open", m["state"])
}

func TestCreateOrUpdateFileCreate(t *testing.T) {
	content := "# Changelog\n\n- v1.2.0\n"
	var putMethod, putPath string
	var captured map[string]any
	getCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			// sha lookup — file does not exist yet → 404 (create path).
			getCalled = true
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(map[string]any{"message": "Not Found"})
		case "PUT":
			putMethod, putPath = r.Method, r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&captured)
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"commit":  map[string]any{"sha": "commitsha"},
				"content": map[string]any{"path": "docs/CHANGELOG.md", "html_url": "https://github.com/octocat/hello/blob/main/docs/CHANGELOG.md"},
			})
		}
	}))
	t.Cleanup(srv.Close)

	c := newCtx(
		map[string]string{"base_url": srv.URL, "token": "ghp_test"},
		map[string]string{"owner": "octocat", "repo": "hello", "path": "docs/CHANGELOG.md", "content": content, "message": "docs: update changelog", "branch": "main"},
	)
	result, err := createOrUpdateFile(c)
	require.NoError(t, err)
	assert.True(t, getCalled, "expected a sha-lookup GET before the PUT")
	assert.Equal(t, "PUT", putMethod)
	assert.Equal(t, "/repos/octocat/hello/contents/docs/CHANGELOG.md", putPath)

	// Content must be base64-encoded in the PUT body, and no sha on create.
	assert.Equal(t, base64.StdEncoding.EncodeToString([]byte(content)), captured["content"])
	assert.Equal(t, "docs: update changelog", captured["message"])
	assert.Equal(t, "main", captured["branch"])
	_, hasSha := captured["sha"]
	assert.False(t, hasSha, "create path must not send a sha")

	m := result.(map[string]any)
	commit := m["commit"].(map[string]any)
	assert.Equal(t, "commitsha", commit["sha"])
}

func TestCreateOrUpdateFileUpdate(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			// existing file → return its blob sha (update path).
			w.WriteHeader(200)
			_ = json.NewEncoder(w).Encode(map[string]any{"sha": "existingsha", "path": "README.md"})
		case "PUT":
			_ = json.NewDecoder(r.Body).Decode(&captured)
			w.WriteHeader(200)
			_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]any{"sha": "newcommit"}})
		}
	}))
	t.Cleanup(srv.Close)

	c := newCtx(
		map[string]string{"base_url": srv.URL, "token": "ghp_test"},
		map[string]string{"owner": "octocat", "repo": "hello", "path": "README.md", "content": "hi", "message": "update"},
	)
	_, err := createOrUpdateFile(c)
	require.NoError(t, err)
	assert.Equal(t, "existingsha", captured["sha"], "update path must carry the discovered sha")
}

// captureSrv records the method, path, and raw query of the last request,
// decodes any JSON body into the supplied pointer, and replies with status+body.
func captureSrv(t *testing.T, status int, respBody any, gotMethod, gotPath, gotQuery *string, captured *map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotMethod, *gotPath, *gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		if captured != nil {
			_ = json.NewDecoder(r.Body).Decode(captured)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if respBody != nil {
			_ = json.NewEncoder(w).Encode(respBody)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGetRepo(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, map[string]any{"full_name": "octocat/hello", "default_branch": "main"}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"}, map[string]string{"owner": "octocat", "repo": "hello"})
	res, err := getRepo(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/repos/octocat/hello", p)
	assert.Equal(t, "octocat/hello", res.(map[string]any)["full_name"])
}

func TestListBranches(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, []map[string]any{{"name": "main"}}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"}, map[string]string{"owner": "octocat", "repo": "hello"})
	res, err := listBranches(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/repos/octocat/hello/branches", p)
	assert.Len(t, res.([]any), 1)
}

func TestListCommits(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, []map[string]any{{"sha": "abc"}}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "sha": "main", "path": "go.mod", "author": "yoga"})
	_, err := listCommits(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/repos/octocat/hello/commits", p)
	assert.Contains(t, q, "sha=main")
	assert.Contains(t, q, "path=go.mod")
	assert.Contains(t, q, "author=yoga")
}

func TestListForks(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, []map[string]any{{"full_name": "fork/hello"}}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"}, map[string]string{"owner": "octocat", "repo": "hello"})
	res, err := listForks(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/repos/octocat/hello/forks", p)
	assert.Len(t, res.([]any), 1)
}

func TestCreateFork(t *testing.T) {
	var m, p, q string
	captured := map[string]any{}
	srv := captureSrv(t, 202, map[string]any{"full_name": "my-org/hello"}, &m, &p, &q, &captured)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "organization": "my-org", "name": "hello"})
	_, err := createFork(c)
	require.NoError(t, err)
	assert.Equal(t, "POST", m)
	assert.Equal(t, "/repos/octocat/hello/forks", p)
	assert.Equal(t, "my-org", captured["organization"])
	assert.Equal(t, "hello", captured["name"])
}

func TestListStargazers(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, []map[string]any{{"login": "yoga"}}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"}, map[string]string{"owner": "octocat", "repo": "hello"})
	res, err := listStargazers(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/repos/octocat/hello/stargazers", p)
	assert.Len(t, res.([]any), 1)
}

func TestStarRepo(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 204, nil, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"}, map[string]string{"owner": "octocat", "repo": "hello"})
	res, err := starRepo(c)
	require.NoError(t, err)
	assert.Equal(t, "PUT", m)
	assert.Equal(t, "/user/starred/octocat/hello", p)
	assert.Equal(t, true, res.(map[string]any)["ok"])
}

func TestUnstarRepo(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 204, nil, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"}, map[string]string{"owner": "octocat", "repo": "hello"})
	res, err := unstarRepo(c)
	require.NoError(t, err)
	assert.Equal(t, "DELETE", m)
	assert.Equal(t, "/user/starred/octocat/hello", p)
	assert.Equal(t, true, res.(map[string]any)["ok"])
}

func TestGetIssue(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, map[string]any{"number": float64(42), "state": "open"}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"}, map[string]string{"owner": "octocat", "repo": "hello", "number": "42"})
	res, err := getIssue(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/repos/octocat/hello/issues/42", p)
	assert.Equal(t, "open", res.(map[string]any)["state"])
}

func TestUpdateIssue(t *testing.T) {
	var m, p, q string
	captured := map[string]any{}
	srv := captureSrv(t, 200, map[string]any{"number": float64(42), "state": "closed"}, &m, &p, &q, &captured)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "number": "42", "state": "closed", "labels": "bug,wontfix"})
	_, err := updateIssue(c)
	require.NoError(t, err)
	assert.Equal(t, "PATCH", m)
	assert.Equal(t, "/repos/octocat/hello/issues/42", p)
	assert.Equal(t, "closed", captured["state"])
	assert.ElementsMatch(t, []any{"bug", "wontfix"}, captured["labels"])
	_, hasTitle := captured["title"]
	assert.False(t, hasTitle, "omitted fields must not be sent")
}

func TestListIssueComments(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, []map[string]any{{"id": float64(1), "body": "hi"}}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"}, map[string]string{"owner": "octocat", "repo": "hello", "number": "42"})
	res, err := listIssueComments(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/repos/octocat/hello/issues/42/comments", p)
	assert.Len(t, res.([]any), 1)
}

func TestGetPR(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, map[string]any{"number": float64(7), "state": "open"}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"}, map[string]string{"owner": "octocat", "repo": "hello", "number": "7"})
	res, err := getPR(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/repos/octocat/hello/pulls/7", p)
	assert.Equal(t, "open", res.(map[string]any)["state"])
}

func TestListPRFiles(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, []map[string]any{{"filename": "main.go", "additions": float64(3), "deletions": float64(1), "status": "modified"}}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"}, map[string]string{"owner": "octocat", "repo": "hello", "number": "7"})
	res, err := listPRFiles(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/repos/octocat/hello/pulls/7/files", p)
	assert.Equal(t, "main.go", res.([]any)[0].(map[string]any)["filename"])
}

func TestUpdatePR(t *testing.T) {
	var m, p, q string
	captured := map[string]any{}
	srv := captureSrv(t, 200, map[string]any{"number": float64(7), "state": "open"}, &m, &p, &q, &captured)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "number": "7", "title": "New title", "base": "develop"})
	_, err := updatePR(c)
	require.NoError(t, err)
	assert.Equal(t, "PATCH", m)
	assert.Equal(t, "/repos/octocat/hello/pulls/7", p)
	assert.Equal(t, "New title", captured["title"])
	assert.Equal(t, "develop", captured["base"])
}

func TestListReleases(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, []map[string]any{{"id": float64(1), "tag_name": "v1.0.0"}}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"}, map[string]string{"owner": "octocat", "repo": "hello"})
	res, err := listReleases(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/repos/octocat/hello/releases", p)
	assert.Len(t, res.([]any), 1)
}

func TestGetLatestRelease(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, map[string]any{"tag_name": "v1.2.0"}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"}, map[string]string{"owner": "octocat", "repo": "hello"})
	res, err := getLatestRelease(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/repos/octocat/hello/releases/latest", p)
	assert.Equal(t, "v1.2.0", res.(map[string]any)["tag_name"])
}

func TestGetRelease(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, map[string]any{"id": float64(123456), "tag_name": "v1.0.0"}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"}, map[string]string{"owner": "octocat", "repo": "hello", "release_id": "123456"})
	_, err := getRelease(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/repos/octocat/hello/releases/123456", p)
}

func TestCreateRelease(t *testing.T) {
	var m, p, q string
	captured := map[string]any{}
	srv := captureSrv(t, 201, map[string]any{"id": float64(99), "html_url": "https://github.com/octocat/hello/releases/tag/v1.2.0"}, &m, &p, &q, &captured)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "tag_name": "v1.2.0", "name": "v1.2.0", "body": "notes", "draft": "true", "prerelease": "false"})
	res, err := createRelease(c)
	require.NoError(t, err)
	assert.Equal(t, "POST", m)
	assert.Equal(t, "/repos/octocat/hello/releases", p)
	assert.Equal(t, "v1.2.0", captured["tag_name"])
	assert.Equal(t, true, captured["draft"])
	_, hasPre := captured["prerelease"]
	assert.False(t, hasPre, "prerelease=false must not be sent")
	assert.Equal(t, float64(99), res.(map[string]any)["id"])
}

func TestUpdateRelease(t *testing.T) {
	var m, p, q string
	captured := map[string]any{}
	srv := captureSrv(t, 200, map[string]any{"id": float64(123456)}, &m, &p, &q, &captured)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "release_id": "123456", "name": "v1.2.1", "body": "patched"})
	_, err := updateRelease(c)
	require.NoError(t, err)
	assert.Equal(t, "PATCH", m)
	assert.Equal(t, "/repos/octocat/hello/releases/123456", p)
	assert.Equal(t, "v1.2.1", captured["name"])
	assert.Equal(t, "patched", captured["body"])
}

func TestDeleteRelease(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 204, nil, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"}, map[string]string{"owner": "octocat", "repo": "hello", "release_id": "123456"})
	res, err := deleteRelease(c)
	require.NoError(t, err)
	assert.Equal(t, "DELETE", m)
	assert.Equal(t, "/repos/octocat/hello/releases/123456", p)
	assert.Equal(t, true, res.(map[string]any)["ok"])
}

func TestListTags(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, []map[string]any{{"name": "v1.0.0"}}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"}, map[string]string{"owner": "octocat", "repo": "hello"})
	res, err := listTags(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/repos/octocat/hello/tags", p)
	assert.Len(t, res.([]any), 1)
}

func TestGetMe(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, map[string]any{"login": "yoga", "type": "User"}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"}, map[string]string{})
	res, err := getMe(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/user", p)
	assert.Equal(t, "yoga", res.(map[string]any)["login"])
}

func TestHealthCheckOK(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, map[string]any{"login": "yoga"}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"}, map[string]string{})
	report, err := HealthCheck(c)
	require.NoError(t, err)
	assert.Equal(t, "/user", p)
	require.Len(t, report, 1)
	assert.Equal(t, "auth", report[0].Key)
	assert.True(t, report[0].OK)
}

func TestHealthCheckBadToken(t *testing.T) {
	srv := mockSrv(t, 401, map[string]any{"message": "Bad credentials"})
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "bad"}, map[string]string{})
	report, err := HealthCheck(c)
	require.NoError(t, err)
	require.Len(t, report, 1)
	assert.Equal(t, "auth", report[0].Key)
	assert.False(t, report[0].OK)
	assert.Contains(t, report[0].Reason, "Bad credentials")
}

func TestUpdateComment(t *testing.T) {
	var m, p, q string
	captured := map[string]any{}
	srv := captureSrv(t, 200, map[string]any{"id": float64(123), "body": "edited"}, &m, &p, &q, &captured)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "comment_id": "123", "body": "edited"})
	res, err := updateComment(c)
	require.NoError(t, err)
	assert.Equal(t, "PATCH", m)
	assert.Equal(t, "/repos/octocat/hello/issues/comments/123", p)
	assert.Equal(t, "edited", captured["body"])
	assert.Equal(t, "edited", res.(map[string]any)["body"])
}

func TestDeleteComment(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 204, nil, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "comment_id": "123"})
	res, err := deleteComment(c)
	require.NoError(t, err)
	assert.Equal(t, "DELETE", m)
	assert.Equal(t, "/repos/octocat/hello/issues/comments/123", p)
	assert.Equal(t, true, res.(map[string]any)["ok"])
}

func TestCreateReview(t *testing.T) {
	var m, p, q string
	captured := map[string]any{}
	srv := captureSrv(t, 200, map[string]any{"id": float64(1), "state": "APPROVED"}, &m, &p, &q, &captured)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "number": "7", "event": "APPROVE", "body": "LGTM"})
	_, err := createReview(c)
	require.NoError(t, err)
	assert.Equal(t, "POST", m)
	assert.Equal(t, "/repos/octocat/hello/pulls/7/reviews", p)
	assert.Equal(t, "APPROVE", captured["event"])
	assert.Equal(t, "LGTM", captured["body"])
}

func TestCreateReviewDefaultEvent(t *testing.T) {
	var m, p, q string
	captured := map[string]any{}
	srv := captureSrv(t, 200, map[string]any{"id": float64(1)}, &m, &p, &q, &captured)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "number": "7"})
	_, err := createReview(c)
	require.NoError(t, err)
	assert.Equal(t, "COMMENT", captured["event"])
}

func TestListReviews(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, []map[string]any{{"id": float64(1), "state": "APPROVED"}}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "number": "7"})
	res, err := listReviews(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/repos/octocat/hello/pulls/7/reviews", p)
	assert.Len(t, res.([]any), 1)
}

func TestCreateReviewComment(t *testing.T) {
	var m, p, q string
	captured := map[string]any{}
	srv := captureSrv(t, 201, map[string]any{"id": float64(9), "path": "main.go"}, &m, &p, &q, &captured)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "number": "7", "body": "nit", "commit_id": "abc123", "path": "main.go", "line": "10"})
	_, err := createReviewComment(c)
	require.NoError(t, err)
	assert.Equal(t, "POST", m)
	assert.Equal(t, "/repos/octocat/hello/pulls/7/comments", p)
	assert.Equal(t, "abc123", captured["commit_id"])
	assert.Equal(t, "main.go", captured["path"])
	assert.Equal(t, float64(10), captured["line"])
	assert.Equal(t, "RIGHT", captured["side"])
}

func TestListReviewComments(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, []map[string]any{{"id": float64(1), "path": "main.go"}}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "number": "7"})
	res, err := listReviewComments(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/repos/octocat/hello/pulls/7/comments", p)
	assert.Len(t, res.([]any), 1)
}

func TestRequestReviewers(t *testing.T) {
	var m, p, q string
	captured := map[string]any{}
	srv := captureSrv(t, 201, map[string]any{"requested_reviewers": []any{}}, &m, &p, &q, &captured)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "number": "7", "reviewers": "yoga,riska"})
	_, err := requestReviewers(c)
	require.NoError(t, err)
	assert.Equal(t, "POST", m)
	assert.Equal(t, "/repos/octocat/hello/pulls/7/requested_reviewers", p)
	assert.ElementsMatch(t, []any{"yoga", "riska"}, captured["reviewers"])
}

func TestCreateBranch(t *testing.T) {
	var postMethod, postPath string
	var captured map[string]any
	refLooked := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/octocat/hello/git/ref/heads/main":
			refLooked = true
			w.WriteHeader(200)
			_ = json.NewEncoder(w).Encode(map[string]any{"object": map[string]any{"sha": "headsha123"}})
		case r.Method == "POST":
			postMethod, postPath = r.Method, r.URL.Path
			_ = json.NewDecoder(r.Body).Decode(&captured)
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]any{"ref": "refs/heads/feature/x", "object": map[string]any{"sha": "headsha123"}})
		}
	}))
	t.Cleanup(srv.Close)

	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "branch": "feature/x", "from_branch": "main"})
	res, err := createBranch(c)
	require.NoError(t, err)
	assert.True(t, refLooked, "expected a ref-lookup GET before the POST")
	assert.Equal(t, "POST", postMethod)
	assert.Equal(t, "/repos/octocat/hello/git/refs", postPath)
	assert.Equal(t, "refs/heads/feature/x", captured["ref"])
	assert.Equal(t, "headsha123", captured["sha"])
	assert.Equal(t, "refs/heads/feature/x", res.(map[string]any)["ref"])
}

func TestDeleteRef(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 204, nil, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "branch": "feature/x"})
	res, err := deleteRef(c)
	require.NoError(t, err)
	assert.Equal(t, "DELETE", m)
	assert.Equal(t, "/repos/octocat/hello/git/refs/heads/feature/x", p)
	assert.Equal(t, true, res.(map[string]any)["ok"])
}

func TestAddLabels(t *testing.T) {
	var m, p, q string
	captured := map[string]any{}
	srv := captureSrv(t, 200, []map[string]any{{"name": "bug"}}, &m, &p, &q, &captured)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "number": "42", "labels": "bug,priority:high"})
	_, err := addLabels(c)
	require.NoError(t, err)
	assert.Equal(t, "POST", m)
	assert.Equal(t, "/repos/octocat/hello/issues/42/labels", p)
	assert.ElementsMatch(t, []any{"bug", "priority:high"}, captured["labels"])
}

func TestRemoveLabel(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, []map[string]any{}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "number": "42", "name": "bug"})
	_, err := removeLabel(c)
	require.NoError(t, err)
	assert.Equal(t, "DELETE", m)
	assert.Equal(t, "/repos/octocat/hello/issues/42/labels/bug", p)
}

func TestAddAssignees(t *testing.T) {
	var m, p, q string
	captured := map[string]any{}
	srv := captureSrv(t, 201, map[string]any{"number": float64(42)}, &m, &p, &q, &captured)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "number": "42", "assignees": "yoga,riska"})
	_, err := addAssignees(c)
	require.NoError(t, err)
	assert.Equal(t, "POST", m)
	assert.Equal(t, "/repos/octocat/hello/issues/42/assignees", p)
	assert.ElementsMatch(t, []any{"yoga", "riska"}, captured["assignees"])
}

func TestGetCommit(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, map[string]any{"sha": "abc123"}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "sha": "abc123"})
	res, err := getCommit(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/repos/octocat/hello/commits/abc123", p)
	assert.Equal(t, "abc123", res.(map[string]any)["sha"])
}

func TestCompareCommits(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, map[string]any{"status": "ahead", "ahead_by": float64(3)}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "base": "main", "head": "feature/x"})
	res, err := compareCommits(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/repos/octocat/hello/compare/main...feature/x", p)
	assert.Equal(t, "ahead", res.(map[string]any)["status"])
}

func TestSearchIssues(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, map[string]any{"total_count": float64(1), "items": []any{map[string]any{"number": float64(1)}}}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"q": "repo:abc/web is:open", "per_page": "10"})
	res, err := searchIssues(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/search/issues", p)
	assert.Contains(t, q, "q=repo%3Aabc%2Fweb+is%3Aopen")
	assert.Equal(t, float64(1), res.(map[string]any)["total_count"])
}

func TestSearchRepos(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, map[string]any{"total_count": float64(2)}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"q": "language:go"})
	_, err := searchRepos(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/search/repositories", p)
	assert.Contains(t, q, "q=language%3Ago")
}

func TestSearchCode(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, map[string]any{"total_count": float64(3)}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"q": "addClass in:file repo:abc/web"})
	_, err := searchCode(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/search/code", p)
	assert.Contains(t, q, "q=addClass")
}

func TestListCollaborators(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, []map[string]any{{"login": "yoga"}}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello"})
	res, err := listCollaborators(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/repos/octocat/hello/collaborators", p)
	assert.Len(t, res.([]any), 1)
}

func TestCreateRepoUser(t *testing.T) {
	var m, p, q string
	captured := map[string]any{}
	srv := captureSrv(t, 201, map[string]any{"full_name": "yoga/web"}, &m, &p, &q, &captured)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"name": "web", "private": "true", "auto_init": "true"})
	res, err := createRepo(c)
	require.NoError(t, err)
	assert.Equal(t, "POST", m)
	assert.Equal(t, "/user/repos", p)
	assert.Equal(t, "web", captured["name"])
	assert.Equal(t, true, captured["private"])
	assert.Equal(t, true, captured["auto_init"])
	assert.Equal(t, "yoga/web", res.(map[string]any)["full_name"])
}

func TestCreateRepoOrg(t *testing.T) {
	var m, p, q string
	captured := map[string]any{}
	srv := captureSrv(t, 201, map[string]any{"full_name": "my-org/web"}, &m, &p, &q, &captured)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"name": "web", "org": "my-org"})
	_, err := createRepo(c)
	require.NoError(t, err)
	assert.Equal(t, "POST", m)
	assert.Equal(t, "/orgs/my-org/repos", p)
}

func TestUpdateRepo(t *testing.T) {
	var m, p, q string
	captured := map[string]any{}
	srv := captureSrv(t, 200, map[string]any{"full_name": "octocat/hello"}, &m, &p, &q, &captured)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "description": "Updated", "private": "true"})
	_, err := updateRepo(c)
	require.NoError(t, err)
	assert.Equal(t, "PATCH", m)
	assert.Equal(t, "/repos/octocat/hello", p)
	assert.Equal(t, "Updated", captured["description"])
	assert.Equal(t, true, captured["private"])
	_, hasArchived := captured["archived"]
	assert.False(t, hasArchived, "archived must not be sent when input is empty")
}

func TestListWorkflows(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, map[string]any{"total_count": float64(1), "workflows": []any{map[string]any{"id": float64(1)}}}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello"})
	res, err := listWorkflows(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/repos/octocat/hello/actions/workflows", p)
	assert.Equal(t, float64(1), res.(map[string]any)["total_count"])
}

func TestListWorkflowRuns(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, map[string]any{"total_count": float64(2), "workflow_runs": []any{}}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello"})
	res, err := listWorkflowRuns(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/repos/octocat/hello/actions/runs", p)
	assert.Equal(t, float64(2), res.(map[string]any)["total_count"])
}

func TestDispatchWorkflow(t *testing.T) {
	var m, p, q string
	captured := map[string]any{}
	srv := captureSrv(t, 204, nil, &m, &p, &q, &captured)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "workflow_id": "ci.yml", "ref": "main", "inputs": `{"env":"prod"}`})
	res, err := dispatchWorkflow(c)
	require.NoError(t, err)
	assert.Equal(t, "POST", m)
	assert.Equal(t, "/repos/octocat/hello/actions/workflows/ci.yml/dispatches", p)
	assert.Equal(t, "main", captured["ref"])
	assert.Equal(t, map[string]any{"env": "prod"}, captured["inputs"])
	assert.Equal(t, true, res.(map[string]any)["ok"])
}

func TestListHooks(t *testing.T) {
	var m, p, q string
	srv := captureSrv(t, 200, []map[string]any{{"id": float64(1), "name": "web"}}, &m, &p, &q, nil)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello"})
	res, err := listHooks(c)
	require.NoError(t, err)
	assert.Equal(t, "GET", m)
	assert.Equal(t, "/repos/octocat/hello/hooks", p)
	assert.Len(t, res.([]any), 1)
}

func TestCreateHook(t *testing.T) {
	var m, p, q string
	captured := map[string]any{}
	srv := captureSrv(t, 201, map[string]any{"id": float64(5), "active": true}, &m, &p, &q, &captured)
	c := newCtx(map[string]string{"base_url": srv.URL, "token": "x"},
		map[string]string{"owner": "octocat", "repo": "hello", "url": "https://example.com/hook", "events": "push,pull_request"})
	_, err := createHook(c)
	require.NoError(t, err)
	assert.Equal(t, "POST", m)
	assert.Equal(t, "/repos/octocat/hello/hooks", p)
	assert.Equal(t, "web", captured["name"])
	assert.Equal(t, true, captured["active"])
	assert.ElementsMatch(t, []any{"push", "pull_request"}, captured["events"])
	config := captured["config"].(map[string]any)
	assert.Equal(t, "https://example.com/hook", config["url"])
	assert.Equal(t, "json", config["content_type"])
}
