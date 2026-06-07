package scm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yogasw/wick/internal/safeexec"
)

// gitInit creates a repo at dir with an initial commit so HEAD exists.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	mustGit(t, dir, "init", "-q", "-b", "main")
	mustGit(t, dir, "config", "user.email", "test@example.com")
	mustGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "-q", "-m", "init")
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := safeexec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func skipNoGit(t *testing.T) {
	t.Helper()
	if _, err := safeexec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
}

func TestDiscoverRepos(t *testing.T) {
	skipNoGit(t)
	root := t.TempDir()
	// Two nested repos + one noise dir that must be skipped.
	repoA := filepath.Join(root, "alpha")
	repoB := filepath.Join(root, "nested", "beta")
	if err := os.MkdirAll(repoA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(repoB, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A fake .git inside node_modules must NOT be discovered.
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "pkg", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, repoA)
	gitInit(t, repoB)

	repos, err := DiscoverRepos(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, r := range repos {
		got[r.Rel] = true
	}
	if !got["alpha"] || !got["nested/beta"] {
		t.Fatalf("expected alpha + nested/beta, got %+v", repos)
	}
	if got["node_modules/pkg"] {
		t.Fatalf("node_modules repo should be skipped, got %+v", repos)
	}
	if len(repos) != 2 {
		t.Fatalf("expected exactly 2 repos, got %d: %+v", len(repos), repos)
	}
}

func TestResolveRepoDir(t *testing.T) {
	skipNoGit(t)
	root := t.TempDir()
	repoA := filepath.Join(root, "alpha")
	if err := os.MkdirAll(repoA, 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, repoA)

	dir, err := ResolveRepoDir(root, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "alpha" {
		t.Fatalf("expected alpha dir, got %s", dir)
	}
	// Bogus handle must be rejected (trust boundary).
	if _, err := ResolveRepoDir(root, "../escape"); err == nil {
		t.Fatal("expected error for bogus repo handle")
	}
	if _, err := ResolveRepoDir(root, "nonexistent"); err != ErrRepoNotFound {
		t.Fatalf("expected ErrRepoNotFound, got %v", err)
	}
}

func TestStatusStageCommit(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	gitInit(t, dir)

	// Modify tracked + add untracked.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st, err := Status(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if st.Branch.Name != "main" {
		t.Fatalf("expected branch main, got %q", st.Branch.Name)
	}
	var sawMod, sawUntracked bool
	for _, c := range st.Changes {
		if c.Path == "README.md" && c.Unstaged {
			sawMod = true
		}
		if c.Path == "new.txt" && c.Untracked {
			sawUntracked = true
		}
	}
	if !sawMod || !sawUntracked {
		t.Fatalf("expected modified README + untracked new.txt, got %+v", st.Changes)
	}

	// Stage both, commit, HEAD advances.
	headBefore := revParse(t, dir)
	if err := Stage(ctx, dir, []string{"README.md", "new.txt"}); err != nil {
		t.Fatal(err)
	}
	st2, _ := Status(ctx, dir)
	for _, c := range st2.Changes {
		if !c.Staged {
			t.Fatalf("expected %s staged, got %+v", c.Path, c)
		}
	}
	sha, err := Commit(ctx, dir, "test commit")
	if err != nil {
		t.Fatal(err)
	}
	if sha == "" {
		t.Fatal("expected non-empty short sha")
	}
	if revParse(t, dir) == headBefore {
		t.Fatal("HEAD did not advance after commit")
	}

	// Empty message rejected.
	if _, err := Commit(ctx, dir, "  "); err == nil {
		t.Fatal("expected error for empty commit message")
	}
}

func TestUnstage(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	gitInit(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Stage(ctx, dir, []string{"README.md"}); err != nil {
		t.Fatal(err)
	}
	if err := Unstage(ctx, dir, []string{"README.md"}); err != nil {
		t.Fatal(err)
	}
	st, _ := Status(ctx, dir)
	for _, c := range st.Changes {
		if c.Path == "README.md" && c.Staged {
			t.Fatalf("README still staged after unstage: %+v", c)
		}
	}
}

func TestBranches(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	gitInit(t, dir)

	if err := CreateBranch(ctx, dir, "feature", true); err != nil {
		t.Fatal(err)
	}
	bl, err := Branches(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if bl.Current != "feature" {
		t.Fatalf("expected current=feature, got %q", bl.Current)
	}
	if err := Checkout(ctx, dir, "main"); err != nil {
		t.Fatal(err)
	}
	bl2, _ := Branches(ctx, dir)
	if bl2.Current != "main" {
		t.Fatalf("expected current=main after checkout, got %q", bl2.Current)
	}
}

func TestDiffAndFileGuard(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	gitInit(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("changed line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	d, err := Diff(ctx, dir, "README.md", false)
	if err != nil {
		t.Fatal(err)
	}
	if d == "" {
		t.Fatal("expected non-empty diff for modified file")
	}

	// WriteFile/ReadFile within repo.
	if err := WriteFile(dir, "sub/x.txt", "hi"); err != nil {
		t.Fatal(err)
	}
	got, err := ReadFile(dir, "sub/x.txt")
	if err != nil || got != "hi" {
		t.Fatalf("read back mismatch: %q %v", got, err)
	}
	// Traversal rejected.
	if _, err := ReadFile(dir, "../escape.txt"); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if err := WriteFile(dir, "../escape.txt", "x"); err == nil {
		t.Fatal("expected write traversal rejection")
	}
}

func TestFileAtRefAndLog(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	gitInit(t, dir) // commit 1: README "init\n"

	// HEAD content of a tracked file.
	head, err := FileAtRef(ctx, dir, "HEAD", "README.md")
	if err != nil {
		t.Fatal(err)
	}
	if head != "init\n" {
		t.Fatalf("HEAD README = %q, want %q", head, "init\n")
	}
	// Missing file at ref → empty, no error.
	missing, err := FileAtRef(ctx, dir, "HEAD", "nope.txt")
	if err != nil || missing != "" {
		t.Fatalf("missing file: %q %v", missing, err)
	}

	// Second commit so Log has 2 entries.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "commit", "-aqm", "second")

	entries, err := Log(ctx, dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 commits, got %d: %+v", len(entries), entries)
	}
	if entries[0].Subject != "second" {
		t.Fatalf("newest subject = %q, want second", entries[0].Subject)
	}
	if entries[0].SHA == "" || entries[0].Author == "" {
		t.Fatalf("commit fields empty: %+v", entries[0])
	}

	// CommitInfo lists the changed file.
	det, err := CommitInfo(ctx, dir, entries[0].SHA)
	if err != nil {
		t.Fatal(err)
	}
	var sawReadme bool
	for _, f := range det.Files {
		if f.Path == "README.md" {
			sawReadme = true
		}
	}
	if !sawReadme {
		t.Fatalf("commit detail missing README: %+v", det.Files)
	}

	// CommitFileDiff non-empty.
	d, err := CommitFileDiff(ctx, dir, entries[0].SHA, "README.md")
	if err != nil {
		t.Fatal(err)
	}
	if d == "" {
		t.Fatal("expected non-empty commit file diff")
	}
}

func TestFileAtIndex(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	gitInit(t, dir) // README "init\n" committed

	// Modify working, stage a DIFFERENT content than HEAD.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Stage(ctx, dir, []string{"README.md"}); err != nil {
		t.Fatal(err)
	}
	// Now change working again (so HEAD != index != working).
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("working\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	head, _ := FileAtRef(ctx, dir, "HEAD", "README.md")
	idx, _ := FileAtIndex(ctx, dir, "README.md")
	work, _ := ReadFile(dir, "README.md")
	if head != "init\n" || idx != "staged\n" || work != "working\n" {
		t.Fatalf("three sides wrong: HEAD=%q index=%q working=%q", head, idx, work)
	}
	// Index of an unknown path → empty, no error.
	if c, err := FileAtIndex(ctx, dir, "nope.txt"); err != nil || c != "" {
		t.Fatalf("missing index file: %q %v", c, err)
	}
}

func TestDiscard(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	gitInit(t, dir) // README "init\n"

	// Modify tracked + add untracked.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Stage the tracked edit too — discard must drop staged + worktree.
	if err := Stage(ctx, dir, []string{"README.md"}); err != nil {
		t.Fatal(err)
	}

	if err := Discard(ctx, dir, []string{"README.md", "new.txt"}, []string{"new.txt"}); err != nil {
		t.Fatal(err)
	}

	// README restored to HEAD content (normalize CRLF: git may apply
	// autocrlf on checkout on Windows).
	got, _ := ReadFile(dir, "README.md")
	if strings.ReplaceAll(got, "\r\n", "\n") != "init\n" {
		t.Fatalf("README not restored: %q", got)
	}
	// Untracked file removed.
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); !os.IsNotExist(err) {
		t.Fatalf("new.txt should be removed, stat err = %v", err)
	}
	// Status clean.
	st, _ := Status(ctx, dir)
	if len(st.Changes) != 0 {
		t.Fatalf("expected clean status after discard, got %+v", st.Changes)
	}
}

func revParse(t *testing.T, dir string) string {
	t.Helper()
	cmd := safeexec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}
