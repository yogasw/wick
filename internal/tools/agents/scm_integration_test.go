package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/registry"
	"github.com/yogasw/wick/internal/agents/session"
)

// TestSCMEndpointsEndToEnd wires the real registry + layout, creates a
// session whose cwd holds a git repo, and drives the git HTTP handlers
// through the test router — exercising the full request path
// (route → session lookup → cwd resolve → repo resolve → git CLI).
func TestSCMEndpointsEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	layout := config.NewLayout(t.TempDir())
	mgr, err := registry.Bootstrap(layout)
	if err != nil {
		t.Fatal(err)
	}
	// Session with no project → cwd falls back to <SessionDir>/cwd.
	sess, err := mgr.CreateSession(context.Background(), session.CreateOptions{ID: "S1", Origin: session.OriginUI})
	if err != nil {
		t.Fatal(err)
	}

	// Wire package globals the handlers read.
	prevMgr, prevLayout := globalMgr, globalLayout
	globalMgr, globalLayout = mgr, layout
	t.Cleanup(func() { globalMgr, globalLayout = prevMgr, prevLayout })

	// Materialize the session cwd and init a repo with a change.
	cwd := filepath.Join(layout.SessionDir(sess.ID), "cwd")
	repo := filepath.Join(cwd, "myrepo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInitRepo(t, repo)
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r := newTestRouter()
	registerSCM(r)

	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/tools/agents"+path, nil)
		w := httptest.NewRecorder()
		r.mux.ServeHTTP(w, req)
		return w
	}
	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/tools/agents"+path, strings.NewReader(body))
		w := httptest.NewRecorder()
		r.mux.ServeHTTP(w, req)
		return w
	}

	// 1) repos lists myrepo with 1 change.
	w := get("/api/sessions/S1/git/repos")
	if w.Code != http.StatusOK {
		t.Fatalf("repos: %d %s", w.Code, w.Body)
	}
	var reposResp struct {
		Repos        []RepoSummary `json:"repos"`
		TotalChanged int           `json:"total_changed"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &reposResp); err != nil {
		t.Fatal(err)
	}
	if len(reposResp.Repos) != 1 || reposResp.Repos[0].Rel != "myrepo" {
		t.Fatalf("expected 1 repo myrepo, got %+v", reposResp.Repos)
	}
	if reposResp.TotalChanged != 1 {
		t.Fatalf("expected total_changed=1, got %d", reposResp.TotalChanged)
	}

	// 2) status shows README modified.
	w = get("/api/sessions/S1/git/status?repo=myrepo")
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
	if !strings.Contains(w.Body.String(), "README.md") {
		t.Fatalf("status missing README: %s", w.Body)
	}

	// 3) bogus repo handle rejected (trust boundary).
	w = get("/api/sessions/S1/git/status?repo=../escape")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bogus repo, got %d %s", w.Code, w.Body)
	}

	// 4) unknown session → 404.
	w = get("/api/sessions/nope/git/repos")
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown session, got %d", w.Code)
	}

	// 5) stage + commit through the HTTP layer.
	w = post("/api/sessions/S1/git/stage", `{"repo":"myrepo","paths":["README.md"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("stage: %d %s", w.Code, w.Body)
	}
	w = post("/api/sessions/S1/git/commit", `{"repo":"myrepo","message":"via http"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("commit: %d %s", w.Code, w.Body)
	}
	var commitResp struct {
		SHA string `json:"sha"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &commitResp)
	if commitResp.SHA == "" {
		t.Fatalf("commit returned empty sha: %s", w.Body)
	}

	// 6) after commit, repos reports 0 changes.
	w = get("/api/sessions/S1/git/repos")
	_ = json.Unmarshal(w.Body.Bytes(), &reposResp)
	if reposResp.TotalChanged != 0 {
		t.Fatalf("expected 0 changes after commit, got %d", reposResp.TotalChanged)
	}
}

func gitInitRepo(t *testing.T, dir string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@e.com"},
		{"config", "user.name", "T"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-q", "-m", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}
