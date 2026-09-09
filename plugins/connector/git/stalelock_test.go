package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A lock left by a process that died mid-operation blocks every later git
// command in that repository until someone deletes it by hand. Clearing it
// is safe only when it is old AND unheld — the test pins both halves,
// because getting the second one wrong corrupts an index.
func TestClearStaleIndexLock(t *testing.T) {
	const lockErr = "Unable to create '/x/.git/index.lock': File exists."

	t.Run("removes an old, unheld lock", func(t *testing.T) {
		repo := repoWithLock(t, time.Now().Add(-2*time.Hour))
		note, ok := clearStaleIndexLock(repo, lockErr)
		if !ok {
			t.Fatal("an unheld two-hour-old lock was not cleared")
		}
		if !strings.Contains(note, "stale") {
			t.Errorf("note = %q, want it to say what was removed", note)
		}
		if _, err := os.Stat(filepath.Join(repo, ".git", "index.lock")); !os.IsNotExist(err) {
			t.Error("lock file still present after a successful clear")
		}
	})

	t.Run("leaves a fresh lock alone", func(t *testing.T) {
		repo := repoWithLock(t, time.Now())
		if _, ok := clearStaleIndexLock(repo, lockErr); ok {
			t.Fatal("cleared a lock a live git command could still be holding")
		}
		if _, err := os.Stat(filepath.Join(repo, ".git", "index.lock")); err != nil {
			t.Errorf("fresh lock was removed anyway: %v", err)
		}
	})

	t.Run("leaves a held lock alone", func(t *testing.T) {
		repo := repoWithLock(t, time.Now().Add(-2*time.Hour))
		lock := filepath.Join(repo, ".git", "index.lock")
		f, err := os.Open(lock)
		if err != nil {
			t.Fatalf("open lock: %v", err)
		}
		defer f.Close()
		if _, ok := clearStaleIndexLock(repo, lockErr); ok {
			t.Fatal("cleared a lock this very process holds open")
		}
	})

	t.Run("ignores failures that are not about the lock", func(t *testing.T) {
		repo := repoWithLock(t, time.Now().Add(-2*time.Hour))
		if _, ok := clearStaleIndexLock(repo, "fatal: could not read Username"); ok {
			t.Fatal("removed a lock in response to an unrelated error")
		}
	})
}

func repoWithLock(t *testing.T, mod time.Time) string {
	t.Helper()
	repo := t.TempDir()
	gitDir := filepath.Join(repo, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	lock := filepath.Join(gitDir, "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	if err := os.Chtimes(lock, mod, mod); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	return repo
}
