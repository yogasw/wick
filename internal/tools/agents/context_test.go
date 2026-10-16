package agents

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yogasw/wick/internal/agents/scm"
)

func TestArtifactServeContentType(t *testing.T) {
	if ct, inline := artifactServeContentType("a.png"); !inline || ct != "image/png" {
		t.Errorf("png inline got ct=%q inline=%v", ct, inline)
	}
	if ct, inline := artifactServeContentType("a.pdf"); !inline || ct != "application/pdf" {
		t.Errorf("pdf inline got ct=%q inline=%v", ct, inline)
	}
	if ct, inline := artifactServeContentType("a.svg"); !inline || ct != "image/svg+xml" {
		t.Errorf("svg inline got ct=%q inline=%v", ct, inline)
	}
	if _, inline := artifactServeContentType("a.html"); inline {
		t.Errorf("html must NOT be inline (download to avoid same-origin exec)")
	}
	if _, inline := artifactServeContentType("a.zip"); inline {
		t.Errorf("zip must NOT be inline")
	}
}

func TestScoreFilePath(t *testing.T) {
	// A missing term rules the path out entirely (AND semantics).
	if _, ok := scoreFilePath("src/main.go", []string{"src", "zzz"}); ok {
		t.Errorf("path with a missing term must not match")
	}
	// All terms present → matches.
	if _, ok := scoreFilePath("src/main.go", []string{"src", "main"}); !ok {
		t.Errorf("path containing every term must match")
	}
	// Empty query matches everything (browsable default).
	if _, ok := scoreFilePath("anything.txt", nil); !ok {
		t.Errorf("empty query must match")
	}
	// A basename hit ranks above a same-term dir-only hit (lower score = better).
	base, _ := scoreFilePath("main.go", []string{"main"})
	dir, _ := scoreFilePath("main/x.go", []string{"main"})
	if !(base < dir) {
		t.Errorf("basename match should rank better: base=%d dir=%d", base, dir)
	}
	// Among equal-quality matches, the shorter path wins.
	short, _ := scoreFilePath("a/util.go", []string{"util"})
	long, _ := scoreFilePath("a/deep/util.go", []string{"util"})
	if !(short < long) {
		t.Errorf("shorter path should rank better: short=%d long=%d", short, long)
	}
}

// A link to a repo root in the projects dir is readable through safeJoin; a
// link to anything else outside base is still refused.
func TestSafeJoinLinkedRepo(t *testing.T) {
	base := t.TempDir()
	projects := filepath.Join(base, "projects")
	cwd := filepath.Join(projects, "a", "files")
	repo := filepath.Join(projects, "b", "files", "tool")
	plain := filepath.Join(projects, "b", "files", "plain")
	outside := filepath.Join(base, "elsewhere", "repo")
	for _, d := range []string{cwd, filepath.Join(repo, ".git"), plain, filepath.Join(outside, ".git")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{"tool": repo, "plain": plain, "out": outside} {
		if err := os.Symlink(target, filepath.Join(cwd, name)); err != nil {
			t.Fatal(err)
		}
	}
	scm.SetLinkRoot(projects)
	t.Cleanup(func() { scm.SetLinkRoot("") })

	realRepo, _ := filepath.EvalSymlinks(repo)
	if got, err := safeJoin(cwd, "tool/.git"); err != nil || got != filepath.Join(realRepo, ".git") {
		t.Fatalf("linked repo: got %q err=%v", got, err)
	}
	for _, rel := range []string{"plain", "out", "out/.git", "tool/../plain"} {
		if _, err := safeJoin(cwd, rel); err == nil {
			t.Fatalf("%s must stay refused", rel)
		}
	}
	scm.SetLinkRoot("")
	if _, err := safeJoin(cwd, "tool"); err == nil {
		t.Fatal("feature off: link must be refused")
	}
}

// A link to a repo in another project is listed as a folder so Files can open
// it; a plain link keeps its file-like entry.
func TestReadDirEntriesLinkedRepoIsDir(t *testing.T) {
	base := t.TempDir()
	projects := filepath.Join(base, "projects")
	cwd := filepath.Join(projects, "a", "files")
	repo := filepath.Join(projects, "b", "files", "tool")
	plain := filepath.Join(projects, "b", "files", "plain")
	for _, d := range []string{cwd, filepath.Join(repo, ".git"), plain} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{"tool": repo, "plain": plain} {
		if err := os.Symlink(target, filepath.Join(cwd, name)); err != nil {
			t.Fatal(err)
		}
	}
	scm.SetLinkRoot(projects)
	t.Cleanup(func() { scm.SetLinkRoot("") })

	got, _ := readDirEntries(cwd, "")
	isDir := map[string]bool{}
	for _, e := range got {
		isDir[e.Name] = e.IsDir
	}
	if !isDir["tool"] || isDir["plain"] {
		t.Fatalf("want tool=dir plain=file, got %v", isDir)
	}
}
