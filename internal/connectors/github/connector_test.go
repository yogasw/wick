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
		"name":    "main.go",
		"content": encoded + "\n",
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
	assert.Len(t, ops, 6)
	keys := make([]string, len(ops))
	for i, op := range ops {
		keys[i] = op.Key
	}
	assert.ElementsMatch(t, []string{"list_repos", "list_issues", "create_issue", "get_file", "list_prs", "add_comment"}, keys)
}
