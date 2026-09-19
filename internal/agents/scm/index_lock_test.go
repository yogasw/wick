package scm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// wick's own git watcher runs `status` on every write in the session tree,
// so a stage that lands in the same millisecond used to fail outright with
// git's "Another git process seems to be running" wall of text — for a
// collision that was over before anyone could read it.
func TestStageWaitsOutAForeignIndexLock(t *testing.T) {
	skipNoGit(t)
	ctx := context.Background()
	dir := t.TempDir()
	gitInit(t, dir)

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(dir, ".git", "index.lock")
	if err := os.WriteFile(lock, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// Somebody else's command finishes shortly after ours starts.
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = os.Remove(lock)
	}()

	start := time.Now()
	if err := Stage(ctx, dir, []string{"a.txt"}); err != nil {
		t.Fatalf("stage should have waited for the lock and succeeded: %v", err)
	}
	if time.Since(start) < 200*time.Millisecond {
		t.Error("stage returned before the lock was released — it cannot have waited")
	}
	st, err := Status(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Changes) != 1 || !st.Changes[0].Staged {
		t.Errorf("status = %+v, want a.txt staged", st.Changes)
	}
}

// A lock nobody releases is a different problem, and the message has to say
// so — including where it is, because wick deliberately does not delete it:
// a stale lock and a held one look identical, and removing the wrong one
// corrupts the index of a repo somebody is mid-operation in.
func TestStageReportsALockThatNeverClears(t *testing.T) {
	skipNoGit(t)
	dir := t.TempDir()
	gitInit(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "index.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// Cancelled rather than waiting out the full budget: this test is about
	// the message, not about the clock.
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()

	err := Stage(ctx, dir, []string{"a.txt"})
	if err == nil {
		t.Fatal("staging into a locked repo reported success")
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".git", "index.lock")); statErr != nil {
		t.Error("wick removed a lock it does not own")
	}
	_ = strings.TrimSpace(err.Error())
}

// The detector must not swallow unrelated failures — a merge conflict that
// got retried for five seconds and then reported as a lock problem would
// send everybody looking in the wrong place.
func TestLockContentionOnlyMatchesTheLock(t *testing.T) {
	if lockContention(&GitError{Stderr: "error: pathspec 'nope' did not match any file(s)"}) {
		t.Error("a pathspec error was mistaken for lock contention")
	}
	if lockContention(nil) {
		t.Error("nil is not contention")
	}
	if !lockContention(&GitError{Stderr: "fatal: Unable to create '/r/.git/index.lock': File exists."}) {
		t.Error("the real message was not recognised")
	}
}
