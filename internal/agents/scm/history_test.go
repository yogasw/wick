package scm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// TestHistoryPaging: page two must continue where page one stopped, and the
// state verdict must survive the jump — the bounded rev-lists are sized by
// Skip+Limit precisely so a commit deep in the history is still found in them.
func TestHistoryPaging(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()

	remote := t.TempDir()
	mustGit(t, remote, "init", "-q", "--bare", "-b", "main")
	dir := t.TempDir()
	gitInit(t, dir)
	for i := 0; i < 5; i++ {
		writeCommit(t, dir, fmt.Sprintf("f%d.txt", i), fmt.Sprintf("commit %d", i))
	}
	mustGit(t, dir, "remote", "add", "origin", remote)
	mustGit(t, dir, "push", "-q", "-u", "origin", "main")
	// Two more that exist only here.
	writeCommit(t, dir, "local1.txt", "local one")
	writeCommit(t, dir, "local2.txt", "local two")

	page1, err := History(ctx, dir, LogOptions{Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	page2, err := History(ctx, dir, LogOptions{Limit: 3, Skip: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(page1) != 3 || len(page2) != 3 {
		t.Fatalf("pages = %d,%d, want 3,3", len(page1), len(page2))
	}
	seen := map[string]bool{}
	for _, e := range append(append([]LogEntry{}, page1...), page2...) {
		if seen[e.SHA] {
			t.Fatalf("commit %s appeared on both pages", e.SHA)
		}
		seen[e.SHA] = true
	}
	// Assert by identity, not by position: these commits are created inside
	// the same second, so git's date ordering interleaves them and "the two
	// newest rows" is not a thing the test can rely on.
	states := map[string]string{}
	for _, e := range append(append([]LogEntry{}, page1...), page2...) {
		states[e.Subject] = e.State
	}
	for _, sub := range []string{"local one", "local two"} {
		if st, ok := states[sub]; ok && st != StateLocal {
			t.Fatalf("%q read as %q, want %q", sub, st, StateLocal)
		}
	}
	for _, sub := range []string{"init", "commit 0", "commit 1"} {
		if st, ok := states[sub]; ok && st == StateLocal {
			t.Fatalf("%q is on the remote but read as local", sub)
		}
	}
	// The whole point of the bound: a pushed commit reached only on page two
	// must still be classified, not fall through to "local" because the
	// rev-list stopped at Limit.
	if len(states) != 6 {
		t.Fatalf("two pages covered %d distinct commits, want 6", len(states))
	}
}

// A commit body may contain the byte we use as a field separator. It used to
// cut the message there and drop the rest — silently, because the fields
// before it still parsed.
func TestCommitBodyKeepsFieldSeparator(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	gitInit(t, dir)

	body := "first line\x1fsecond half after the separator"
	mustGit(t, dir, "commit", "--allow-empty", "-m", "subject line\n\n"+body)

	head, err := History(ctx, dir, LogOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	det, err := CommitInfo(ctx, dir, head[0].SHA)
	if err != nil {
		t.Fatal(err)
	}
	if det.Subject != "subject line" {
		t.Fatalf("subject = %q", det.Subject)
	}
	if !strings.Contains(det.Body, "second half after the separator") {
		t.Fatalf("body was cut at the separator: %q", det.Body)
	}
	if det.Email == "" {
		t.Fatal("email lost")
	}
}
