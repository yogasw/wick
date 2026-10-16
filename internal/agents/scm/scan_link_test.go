package scm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A symlink to a repo root inside the link root is listed as a repo; links to
// a plain folder, to a repo outside the link root, or with the feature off
// are not.
func TestDiscoverReposFollowsRepoLinkInsideLinkRoot(t *testing.T) {
	base := t.TempDir()
	projects := filepath.Join(base, "projects")
	cwd := filepath.Join(projects, "a", "files")
	other := filepath.Join(projects, "b", "files", "tool")
	outside := filepath.Join(base, "elsewhere", "repo")
	plain := filepath.Join(projects, "b", "files", "plain")
	for _, d := range []string{cwd, other, outside, plain} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitInit(t, other)
	gitInit(t, outside)
	for name, target := range map[string]string{"linked": other, "out": outside, "plain": plain} {
		if err := os.Symlink(target, filepath.Join(cwd, name)); err != nil {
			t.Fatal(err)
		}
	}

	SetLinkRoot("")
	got, err := DiscoverRepos(cwd)
	if err != nil || len(got) != 0 {
		t.Fatalf("feature off: want no repos, got %v err=%v", got, err)
	}

	SetLinkRoot(projects)
	t.Cleanup(func() { SetLinkRoot("") })
	got, err = DiscoverRepos(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Rel != "linked" || got[0].Name != "linked" {
		t.Fatalf("want only [linked], got %v", got)
	}
	dir, err := ResolveRepoDir(cwd, "linked")
	if err != nil || dir != filepath.Join(cwd, "linked") {
		t.Fatalf("ResolveRepoDir = %q, %v", dir, err)
	}
	if _, err := Status(context.Background(), dir); err != nil {
		t.Fatalf("git through the link: %v", err)
	}
}
