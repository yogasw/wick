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
	assert.Len(t, ops, 10)
	keys := make([]string, len(ops))
	for i, op := range ops {
		keys[i] = op.Key
	}
	assert.ElementsMatch(t, []string{
		"list_repos", "list_issues", "create_issue", "get_file", "list_prs", "add_comment",
		"get_pr_diff", "merge_pr", "create_pr", "create_or_update_file",
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
