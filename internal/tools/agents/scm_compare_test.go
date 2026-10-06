package agents

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/registry"
	"github.com/yogasw/wick/internal/agents/scm"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/pkg/safeexec"
)

// TestSCMCompareEndpoints drives the Compare tab's five endpoints through
// the real route table: the ref comparison and its per-file sides, then
// each rollback. One repo, walked in order, because the rollbacks change
// the state the next assertion reads — which is also how the panel uses
// them.
func TestSCMCompareEndpoints(t *testing.T) {
	if _, err := safeexec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}

	layout := config.NewLayout(t.TempDir())
	mgr, err := registry.Bootstrap(layout)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := mgr.CreateSession(context.Background(), session.CreateOptions{ID: "SC1", Origin: session.OriginUI})
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

	runGit := func(args ...string) {
		t.Helper()
		cmd := safeexec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// main: one file. side: edits it and adds another.
	write("app.txt", "one\ntwo\n")
	runGit("add", ".")
	runGit("commit", "-qm", "base")
	runGit("checkout", "-q", "-b", "side")
	write("app.txt", "one\ntwo\nthree\n")
	write("added.txt", "new\n")
	runGit("add", ".")
	runGit("commit", "-qm", "side work")

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

	// 1) compare-refs: the file list the tab renders.
	w := get("/api/sessions/SC1/git/compare-refs?repo=myrepo&base=main&head=side&three_dot=1")
	if w.Code != http.StatusOK {
		t.Fatalf("compare-refs: %d %s", w.Code, w.Body)
	}
	var cmp scm.CompareResult
	if err := json.Unmarshal(w.Body.Bytes(), &cmp); err != nil {
		t.Fatal(err)
	}
	if cmp.Ahead != 1 || cmp.Behind != 0 {
		t.Fatalf("ahead/behind = %d/%d, want 1/0: %+v", cmp.Ahead, cmp.Behind, cmp)
	}
	if len(cmp.Files) != 2 {
		t.Fatalf("expected 2 changed files, got %+v", cmp.Files)
	}
	seen := map[string]scm.CompareFile{}
	for _, f := range cmp.Files {
		seen[f.Path] = f
	}
	if f := seen["app.txt"]; f.Status != "M" || f.Additions != 1 {
		t.Fatalf("app.txt = %+v, want M +1", f)
	}
	if f := seen["added.txt"]; f.Status != "A" {
		t.Fatalf("added.txt = %+v, want A", f)
	}

	// Missing refs are a 400, not a 500 or an empty 200.
	if w = get("/api/sessions/SC1/git/compare-refs?repo=myrepo&base=main"); w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 without head, got %d %s", w.Code, w.Body)
	}
	// A ref shaped like a git option never reaches git.
	if w = get("/api/sessions/SC1/git/compare-refs?repo=myrepo&base=--all&head=side"); w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for option-shaped ref, got %d %s", w.Code, w.Body)
	}

	// 2) ref-compare: the two raw sides for one file.
	var sides struct {
		Original string `json:"original"`
		Modified string `json:"modified"`
		Path     string `json:"path"`
	}
	w = get("/api/sessions/SC1/git/ref-compare?repo=myrepo&base=main&head=side&path=app.txt")
	if w.Code != http.StatusOK {
		t.Fatalf("ref-compare: %d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &sides); err != nil {
		t.Fatal(err)
	}
	if sides.Original != "one\ntwo\n" || sides.Modified != "one\ntwo\nthree\n" {
		t.Fatalf("sides = %q / %q", sides.Original, sides.Modified)
	}
	// A file that exists on one side only: empty string, not an error.
	w = get("/api/sessions/SC1/git/ref-compare?repo=myrepo&base=main&head=side&path=added.txt")
	if w.Code != http.StatusOK {
		t.Fatalf("ref-compare added file: %d %s", w.Code, w.Body)
	}
	sides.Original, sides.Modified = "x", "x"
	if err := json.Unmarshal(w.Body.Bytes(), &sides); err != nil {
		t.Fatal(err)
	}
	if sides.Original != "" || sides.Modified != "new\n" {
		t.Fatalf("added file sides = %q / %q, want empty original", sides.Original, sides.Modified)
	}

	// The working tree as head: the right side is the file on disk.
	if err := os.WriteFile(filepath.Join(repo, "app.txt"), []byte("one\ntwo\nthree\nwip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w = get("/api/sessions/SC1/git/ref-compare?repo=myrepo&base=main&head=:worktree&path=app.txt&three_dot=1")
	if w.Code != http.StatusOK {
		t.Fatalf("ref-compare worktree: %d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &sides); err != nil {
		t.Fatal(err)
	}
	if sides.Original != "one\ntwo\n" || sides.Modified != "one\ntwo\nthree\nwip\n" {
		t.Fatalf("worktree sides = %q / %q", sides.Original, sides.Modified)
	}
	w = get("/api/sessions/SC1/git/compare-refs?repo=myrepo&base=main&head=:worktree&three_dot=1")
	if w.Code != http.StatusOK {
		t.Fatalf("compare-refs worktree: %d %s", w.Code, w.Body)
	}
	// Refused refs never reach git: options, ranges, working tree as base.
	for _, q := range []string{
		"base=-p&head=side", "base=main&head=--output%3Dx", "base=main..side&head=side",
		"base=:worktree&head=side", "base=nope&head=side",
	} {
		if w = get("/api/sessions/SC1/git/ref-compare?repo=myrepo&path=app.txt&" + q); w.Code != http.StatusBadRequest {
			t.Fatalf("ref-compare %s: expected 400, got %d %s", q, w.Code, w.Body)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "app.txt"), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 3) restore: put app.txt back to main's content on the side branch.
	w = post("/api/sessions/SC1/git/restore", `{"repo":"myrepo","ref":"main","paths":["app.txt"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", w.Code, w.Body)
	}
	b, err := os.ReadFile(filepath.Join(repo, "app.txt"))
	if err != nil || string(b) != "one\ntwo\n" {
		t.Fatalf("restore did not write main's content: %q %v", b, err)
	}
	// Traversal is refused by the path guard, not by git.
	w = post("/api/sessions/SC1/git/restore", `{"repo":"myrepo","ref":"main","paths":["../escape"]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for traversal path, got %d %s", w.Code, w.Body)
	}

	// Clean the restore back out so the next steps start from a tidy tree.
	runGit("checkout", "-q", "--", "app.txt")
	runGit("reset", "-q", "--hard", "HEAD")

	// 4) revert: undo side's commit; added.txt goes away again.
	var revertResp struct {
		Status string `json:"status"`
	}
	w = post("/api/sessions/SC1/git/revert", `{"repo":"myrepo","sha":"HEAD"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("revert: %d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &revertResp); err != nil {
		t.Fatal(err)
	}
	if revertResp.Status != "reverted" {
		t.Fatalf("revert status = %q", revertResp.Status)
	}
	if _, err := os.Stat(filepath.Join(repo, "added.txt")); !os.IsNotExist(err) {
		t.Fatalf("revert left the added file behind: %v", err)
	}

	// 5) reset: move side back to main, discarding the revert commit too.
	w = post("/api/sessions/SC1/git/reset", `{"repo":"myrepo","ref":"main","mode":"hard"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("reset: %d %s", w.Code, w.Body)
	}
	w = get("/api/sessions/SC1/git/compare-refs?repo=myrepo&base=main&head=side&three_dot=1")
	cmp = scm.CompareResult{}
	if err := json.Unmarshal(w.Body.Bytes(), &cmp); err != nil {
		t.Fatal(err)
	}
	if len(cmp.Files) != 0 || cmp.Ahead != 0 {
		t.Fatalf("after reset the branches still differ: %+v", cmp)
	}
	// An unknown mode is refused before git sees it.
	w = post("/api/sessions/SC1/git/reset", `{"repo":"myrepo","ref":"main","mode":"nonsense"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad reset mode, got %d %s", w.Code, w.Body)
	}
}
