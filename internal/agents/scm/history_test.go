package scm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// writeCommit makes one commit with a unique file so the graph has shape.
func writeCommit(t *testing.T, dir, name, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "-qm", msg)
}

// stateOf finds a commit by subject and returns its state.
func stateOf(t *testing.T, entries []LogEntry, subject string) string {
	t.Helper()
	for _, e := range entries {
		if e.Subject == subject {
			return e.State
		}
	}
	t.Fatalf("commit %q not in history: %+v", subject, entries)
	return ""
}

// TestHistoryStates is the whole point of the graph: a commit that only
// exists here must not look like one that has landed on the trunk.
func TestHistoryStates(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()

	// A bare repo to push to stands in for the remote.
	remote := t.TempDir()
	mustGit(t, remote, "init", "-q", "--bare", "-b", "main")

	dir := t.TempDir()
	gitInit(t, dir) // commit "init" on main
	mustGit(t, dir, "remote", "add", "origin", remote)
	mustGit(t, dir, "push", "-q", "-u", "origin", "main")

	// A feature branch: one commit pushed, one left behind locally.
	mustGit(t, dir, "checkout", "-q", "-b", "feature")
	writeCommit(t, dir, "a.txt", "pushed work")
	mustGit(t, dir, "push", "-q", "-u", "origin", "feature")
	writeCommit(t, dir, "b.txt", "local work")

	entries, err := History(ctx, dir, LogOptions{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, entries, "local work"); got != StateLocal {
		t.Fatalf("unpushed commit state = %q, want %q", got, StateLocal)
	}
	if got := stateOf(t, entries, "pushed work"); got != StatePushed {
		t.Fatalf("pushed-but-not-merged commit state = %q, want %q", got, StatePushed)
	}
	// "init" is what origin/main points at — the trunk.
	if got := stateOf(t, entries, "init"); got != StateTrunk {
		t.Fatalf("trunk commit state = %q, want %q", got, StateTrunk)
	}
}

// TestHistoryRefsAndDecoration covers what the picker and the row badges
// are drawn from.
func TestHistoryRefsAndDecoration(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()

	remote := t.TempDir()
	mustGit(t, remote, "init", "-q", "--bare", "-b", "main")
	dir := t.TempDir()
	gitInit(t, dir)
	mustGit(t, dir, "remote", "add", "origin", remote)
	mustGit(t, dir, "push", "-q", "-u", "origin", "main")
	mustGit(t, dir, "checkout", "-q", "-b", "side")
	writeCommit(t, dir, "s.txt", "side work")

	refs, err := HistoryRefs(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	var sawLocal, sawRemote, sawCurrent bool
	for _, r := range refs {
		if r.Name == "main" && !r.Remote {
			sawLocal = true
		}
		if r.Name == "origin/main" && r.Remote {
			sawRemote = true
		}
		if r.Name == "side" && r.Current {
			sawCurrent = true
		}
		if r.SHA == "" {
			t.Fatalf("ref %q has no sha", r.Name)
		}
	}
	if !sawLocal || !sawRemote || !sawCurrent {
		t.Fatalf("refs missing pieces (local=%v remote=%v current=%v): %+v", sawLocal, sawRemote, sawCurrent, refs)
	}

	// The tip of `side` carries HEAD and the branch name as badges, and
	// parents are populated so lanes can be drawn.
	entries, err := History(ctx, dir, LogOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	tip := entries[0]
	if len(tip.Parents) != 1 {
		t.Fatalf("tip parents = %v, want exactly one", tip.Parents)
	}
	var hasBranch bool
	for _, r := range tip.Refs {
		if r == "side" {
			hasBranch = true
		}
	}
	if !hasBranch {
		t.Fatalf("tip refs = %v, want the branch name among them", tip.Refs)
	}
}

// TestHistoryRefsSelector: "all" reaches a branch the current one cannot,
// which is what the picker's All option is for.
func TestHistoryRefsSelector(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	gitInit(t, dir)
	mustGit(t, dir, "checkout", "-q", "-b", "other")
	writeCommit(t, dir, "o.txt", "only on other")
	mustGit(t, dir, "checkout", "-q", "main")

	auto, err := History(ctx, dir, LogOptions{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range auto {
		if e.Subject == "only on other" {
			t.Fatal("auto walked a branch that is not checked out")
		}
	}

	all, err := History(ctx, dir, LogOptions{Limit: 20, Refs: []string{RefsAll}})
	if err != nil {
		t.Fatal(err)
	}
	if stateOf(t, all, "only on other") != StateLocal {
		t.Fatal("a commit on no remote should read as local")
	}

	// A named ref walks exactly that branch.
	named, err := History(ctx, dir, LogOptions{Limit: 20, Refs: []string{"other"}})
	if err != nil {
		t.Fatal(err)
	}
	if named[0].Subject != "only on other" {
		t.Fatalf("named ref walked the wrong branch: %+v", named[0])
	}

	// An argument that could be read as an option is dropped, not passed.
	safe, err := History(ctx, dir, LogOptions{Limit: 20, Refs: []string{"--output=/tmp/pwn"}})
	if err != nil {
		t.Fatalf("option-looking ref should fall back, not fail: %v", err)
	}
	if len(safe) == 0 {
		t.Fatal("fallback walk returned nothing")
	}
}
