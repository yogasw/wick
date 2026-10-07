package scm

import (
	"context"
	"strings"
	"testing"
)

// TestBranchRenameDeleteAndCreateFrom covers the three actions the panel's ⋯
// menu exposes, including the refusal that keeps a delete from losing work.
func TestBranchRenameDeleteAndCreateFrom(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	gitInit(t, dir) // "init" on main

	// Create from an explicit start point rather than HEAD.
	writeCommit(t, dir, "a.txt", "second")
	if err := CreateBranchFrom(ctx, dir, "from-root", "main~1", false); err != nil {
		t.Fatalf("create from start point: %v", err)
	}
	entries, err := History(ctx, dir, LogOptions{Limit: 10, Refs: []string{"from-root"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Subject != "init" {
		t.Fatalf("branch did not start at the named commit: %+v", entries)
	}

	if err := RenameBranch(ctx, dir, "from-root", "renamed"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	refs, err := HistoryRefs(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	var sawNew, sawOld bool
	for _, r := range refs {
		if r.Name == "renamed" {
			sawNew = true
		}
		if r.Name == "from-root" {
			sawOld = true
		}
	}
	if !sawNew || sawOld {
		t.Fatalf("rename did not take: %+v", refs)
	}

	// A branch whose work is already on main deletes without force.
	if err := DeleteBranch(ctx, dir, "renamed", false); err != nil {
		t.Fatalf("delete merged branch: %v", err)
	}

	// One that carries unmerged work does NOT — that refusal is the guard
	// the UI escalates from, and it must keep working.
	mustGit(t, dir, "checkout", "-q", "-b", "unmerged")
	writeCommit(t, dir, "b.txt", "only here")
	mustGit(t, dir, "checkout", "-q", "main")
	if err := DeleteBranch(ctx, dir, "unmerged", false); err == nil {
		t.Fatal("unmerged branch deleted without force")
	}
	if err := DeleteBranch(ctx, dir, "unmerged", true); err != nil {
		t.Fatalf("forced delete: %v", err)
	}
}

// Names that could be read as a git option must be refused before they reach
// the command line.
func TestBranchNameGuard(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	gitInit(t, dir)

	for _, bad := range []string{"", "   ", "--force", "-D", "has space"} {
		if err := CreateBranchFrom(ctx, dir, bad, "", false); err == nil {
			t.Fatalf("accepted %q as a branch name", bad)
		}
		if err := DeleteBranch(ctx, dir, bad, false); err == nil {
			t.Fatalf("accepted %q for delete", bad)
		}
	}
	if err := CreateBranchFrom(ctx, dir, "ok", "--all", false); err == nil ||
		!strings.Contains(err.Error(), "start point") {
		t.Fatalf("start point guard missing: %v", err)
	}
}
