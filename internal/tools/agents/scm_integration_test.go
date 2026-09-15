package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/registry"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/safeexec"
)

// TestSCMEndpointsEndToEnd wires the real registry + layout, creates a
// session whose cwd holds a git repo, and drives the git HTTP handlers
// through the test router — exercising the full request path
// (route → session lookup → cwd resolve → repo resolve → git CLI).
func TestSCMEndpointsEndToEnd(t *testing.T) {
	if _, err := safeexec.LookPath("git"); err != nil {
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
		cmd := safeexec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-q", "-m", "init"}} {
		cmd := safeexec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// TestSCMGraphEndpoints drives the two endpoints the Source panel's graph
// reads: the log with its reference selector, and the reference list the
// picker is built from.
func TestSCMGraphEndpoints(t *testing.T) {
	if _, err := safeexec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	layout := config.NewLayout(t.TempDir())
	mgr, err := registry.Bootstrap(layout)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := mgr.CreateSession(context.Background(), session.CreateOptions{ID: "S2", Origin: session.OriginUI})
	if err != nil {
		t.Fatal(err)
	}
	prevMgr, prevLayout := globalMgr, globalLayout
	globalMgr, globalLayout = mgr, layout
	t.Cleanup(func() { globalMgr, globalLayout = prevMgr, prevLayout })

	cwd := filepath.Join(layout.SessionDir(sess.ID), "cwd")
	repo := filepath.Join(cwd, "myrepo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInitRepo(t, repo)

	// A commit on a branch that is NOT checked out, so "auto" and "all"
	// have to disagree.
	runGit := func(args ...string) {
		t.Helper()
		cmd := safeexec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("checkout", "-q", "-b", "side")
	if err := os.WriteFile(filepath.Join(repo, "side.txt"), []byte("side\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".")
	runGit("commit", "-qm", "side only")
	runGit("checkout", "-q", "main")

	r := newTestRouter()
	registerSCM(r)
	get := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/tools/agents"+path, nil)
		w := httptest.NewRecorder()
		r.mux.ServeHTTP(w, req)
		return w
	}

	// Default walk: the checked-out branch only, and its commit is local
	// because this repo has no remote at all.
	var logResp struct {
		Commits []struct {
			Subject string   `json:"subject"`
			State   string   `json:"state"`
			Parents []string `json:"parents"`
		} `json:"commits"`
	}
	w := get("/api/sessions/S2/git/log?repo=myrepo&limit=20")
	if w.Code != http.StatusOK {
		t.Fatalf("log code = %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &logResp); err != nil {
		t.Fatal(err)
	}
	if len(logResp.Commits) != 1 {
		t.Fatalf("auto walked %d commits, want just the checked-out branch", len(logResp.Commits))
	}
	if logResp.Commits[0].State != "local" {
		t.Fatalf("state = %q, want local for a repo with no remote", logResp.Commits[0].State)
	}

	// refs=all reaches the other branch.
	w = get("/api/sessions/S2/git/log?repo=myrepo&limit=20&refs=all")
	logResp.Commits = nil
	if err := json.Unmarshal(w.Body.Bytes(), &logResp); err != nil {
		t.Fatal(err)
	}
	var sawSide bool
	for _, c := range logResp.Commits {
		if c.Subject == "side only" {
			sawSide = true
		}
	}
	if !sawSide {
		t.Fatalf("refs=all missed the unchecked-out branch: %+v", logResp.Commits)
	}

	// The picker's source.
	var refsResp struct {
		Refs []struct {
			Name    string `json:"name"`
			SHA     string `json:"sha"`
			Current bool   `json:"current"`
		} `json:"refs"`
	}
	w = get("/api/sessions/S2/git/refs?repo=myrepo")
	if w.Code != http.StatusOK {
		t.Fatalf("refs code = %d: %s", w.Code, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &refsResp); err != nil {
		t.Fatal(err)
	}
	var current string
	names := map[string]bool{}
	for _, r := range refsResp.Refs {
		names[r.Name] = true
		if r.Current {
			current = r.Name
		}
	}
	if !names["main"] || !names["side"] {
		t.Fatalf("refs missing branches: %+v", refsResp.Refs)
	}
	if current != "main" {
		t.Fatalf("current ref = %q, want main", current)
	}
}

// TestProjectOptionsIncludesPinnedWorkspace: a workflow's saved workspace has
// to come back even when the caller cannot reach that project, or the editor's
// picker silently reads as "(use run workspace)" for a workflow that is in
// fact pinned — one person's saved choice misreported to the next.
func TestProjectOptionsIncludesPinnedWorkspace(t *testing.T) {
	layout := config.NewLayout(t.TempDir())
	mgr, err := registry.Bootstrap(layout)
	if err != nil {
		t.Fatal(err)
	}
	prevMgr, prevLayout, prevPool := globalMgr, globalLayout, globalPool
	globalMgr, globalLayout = mgr, layout
	// notReady gates on the pool too; the handler never touches it here.
	globalPool = pool.New(pool.PoolConfig{Layout: layout})
	t.Cleanup(func() { globalMgr, globalLayout, globalPool = prevMgr, prevLayout, prevPool })

	// Two projects: one the caller owns, one owned by somebody else.
	if _, err := mgr.CreateProject(context.Background(), project.CreateOptions{ID: "mine", Name: "Mine"}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.CreateProject(context.Background(), project.CreateOptions{ID: "theirs", Name: "Theirs"}); err != nil {
		t.Fatal(err)
	}
	theirs, _ := mgr.Registry().Project("theirs")
	meta := theirs.Meta
	meta.OwnerUserID = "someone-else"
	if _, err := mgr.UpdateProject(context.Background(), "theirs", meta); err != nil {
		t.Fatal(err)
	}
	mine, _ := mgr.Registry().Project("mine")
	mmeta := mine.Meta
	mmeta.OwnerUserID = "u-caller"
	if _, err := mgr.UpdateProject(context.Background(), "mine", mmeta); err != nil {
		t.Fatal(err)
	}

	r := newTestRouter()
	r.GET("/projects/options", projectOptionsJSON)
	caller := &entity.User{ID: "u-caller", Email: "c@x.test", Name: "Caller", Approved: true, Role: entity.RoleUser}

	get := func(path string) []map[string]any {
		req := httptest.NewRequest("GET", "/tools/agents"+path, nil)
		req = req.WithContext(login.WithUser(req.Context(), caller, nil))
		w := httptest.NewRecorder()
		r.mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("code = %d: %s", w.Code, w.Body.String())
		}
		var out []map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	ids := func(opts []map[string]any) map[string]bool {
		m := map[string]bool{}
		for _, o := range opts {
			m[o["id"].(string)] = true
		}
		return m
	}

	// Plain listing: only what the caller reaches.
	plain := ids(get("/projects/options"))
	if !plain["mine"] || plain["theirs"] {
		t.Fatalf("plain options = %v, want only the caller's own project", plain)
	}

	// Asked for by id: present, and flagged so the UI can say why.
	withPinned := get("/projects/options?include=theirs")
	if !ids(withPinned)["theirs"] {
		t.Fatalf("include= did not return the pinned project: %v", withPinned)
	}
	for _, o := range withPinned {
		switch o["id"] {
		case "theirs":
			if o["no_access"] != true {
				t.Fatalf("pinned project should be flagged no_access: %v", o)
			}
		case "mine":
			if _, flagged := o["no_access"]; flagged {
				t.Fatalf("an accessible project must not be flagged: %v", o)
			}
		}
	}
}

// TestSCMLogPaging proves the contract the panel's infinite scroll depends on:
// skip shifts the window, has_more says whether to ask again, and the last
// page reports has_more=false so the list stops asking.
func TestSCMLogPaging(t *testing.T) {
	if _, err := safeexec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	layout := config.NewLayout(t.TempDir())
	mgr, err := registry.Bootstrap(layout)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := mgr.CreateSession(context.Background(), session.CreateOptions{ID: "S3", Origin: session.OriginUI})
	if err != nil {
		t.Fatal(err)
	}
	prevMgr, prevLayout := globalMgr, globalLayout
	globalMgr, globalLayout = mgr, layout
	t.Cleanup(func() { globalMgr, globalLayout = prevMgr, prevLayout })

	repo := filepath.Join(layout.SessionDir(sess.ID), "cwd", "myrepo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInitRepo(t, repo)
	for i := 0; i < 4; i++ {
		if err := os.WriteFile(filepath.Join(repo, "f.txt"), []byte(strings.Repeat("x", i+1)), 0o644); err != nil {
			t.Fatal(err)
		}
		// -a only picks up TRACKED files; f.txt is new on the first pass.
		for _, args := range [][]string{{"add", "."}, {"commit", "-qm", "step"}} {
			cmd := safeexec.Command("git", args...)
			cmd.Dir = repo
			if out, cerr := cmd.CombinedOutput(); cerr != nil {
				t.Fatalf("git %v: %v\n%s", args, cerr, out)
			}
		}
	}

	r := newTestRouter()
	registerSCM(r)
	page := func(limit, skip int) (shas []string, more bool) {
		req := httptest.NewRequest("GET",
			"/tools/agents/api/sessions/S3/git/log?repo=myrepo&limit="+strconv.Itoa(limit)+"&skip="+strconv.Itoa(skip), nil)
		w := httptest.NewRecorder()
		r.mux.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("code = %d: %s", w.Code, w.Body.String())
		}
		var resp struct {
			Commits []struct {
				SHA string `json:"sha"`
			} `json:"commits"`
			HasMore bool `json:"has_more"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatal(err)
		}
		for _, c := range resp.Commits {
			shas = append(shas, c.SHA)
		}
		return shas, resp.HasMore
	}

	first, more := page(2, 0)
	if len(first) != 2 || !more {
		t.Fatalf("page 1 = %d commits, has_more=%v; want 2 and true", len(first), more)
	}
	second, _ := page(2, 2)
	if len(second) != 2 {
		t.Fatalf("page 2 = %d commits, want 2", len(second))
	}
	for _, a := range first {
		for _, b := range second {
			if a == b {
				t.Fatalf("commit %s served on both pages", a)
			}
		}
	}
	// Five commits total (init + 4): the third page holds the last one and
	// must NOT claim there is more, or the panel keeps asking forever.
	last, more := page(2, 4)
	if len(last) != 1 || more {
		t.Fatalf("last page = %d commits, has_more=%v; want 1 and false", len(last), more)
	}
}
