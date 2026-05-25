package providersync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/entity"
)

// waitFor polls fn() up to timeout, returning when it returns true.
// Watcher events arrive asynchronously through the OS, so we can't
// pin a deterministic order — poll-with-timeout is the standard
// pattern for these tests.
func waitFor(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waitFor: condition still false after %v", timeout)
}

func TestWatcher_DetectsNewFile(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()
	dir := t.TempDir()

	if _, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i",
		Label: "folder", SyncPath: dir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save source: %v", err)
	}
	// Short debounce so the test runs fast.
	if err := mgr.EnsureWatcher(ctx, 100); err != nil {
		t.Fatalf("EnsureWatcher: %v", err)
	}
	defer mgr.StopWatcher()

	target := filepath.Join(dir, "new.txt")
	if err := os.WriteFile(target, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	want := filepath.ToSlash(target)
	waitFor(t, 3*time.Second, func() bool {
		rows, _ := mgr.ListAll(ctx)
		for _, r := range rows {
			if !r.IsDir && r.RelPath == want {
				return true
			}
		}
		return false
	})
}

func TestWatcher_HardDeletesOnRemove(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()
	dir := t.TempDir()

	target := filepath.Join(dir, "x.txt")
	writeFile(t, target, "x")

	if _, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i",
		Label: "folder", SyncPath: dir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save source: %v", err)
	}
	if err := mgr.EnsureWatcher(ctx, 100); err != nil {
		t.Fatalf("EnsureWatcher: %v", err)
	}
	defer mgr.StopWatcher()

	want := filepath.ToSlash(target)
	// Initial SaveSource already triggered a sync — wait for the row.
	waitFor(t, 3*time.Second, func() bool {
		rows, _ := mgr.ListAll(ctx)
		for _, r := range rows {
			if !r.IsDir && r.RelPath == want {
				return true
			}
		}
		return false
	})

	if err := os.Remove(target); err != nil {
		t.Fatalf("remove: %v", err)
	}

	// Row must disappear quickly — Remove is immediate, no debounce.
	waitFor(t, 3*time.Second, func() bool {
		rows, _ := mgr.ListAll(ctx)
		for _, r := range rows {
			if !r.IsDir && r.RelPath == want {
				return false
			}
		}
		return true
	})
}

func TestWatcher_RespectsExcludes(t *testing.T) {
	db := newDB(t)
	mgr := New(db)
	ctx := context.Background()
	dir := t.TempDir()
	subDir := filepath.Join(dir, "skipme")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if _, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i",
		Label: "folder", SyncPath: dir, Mode: "folder", Enabled: true,
	}); err != nil {
		t.Fatalf("save folder: %v", err)
	}
	if _, err := mgr.SaveSource(ctx, entity.ProviderStorageSource{
		ProviderType: "p", InstanceName: "i",
		Mode: "exclude", SyncPath: filepath.ToSlash(subDir), Enabled: true,
	}); err != nil {
		t.Fatalf("save exclude: %v", err)
	}
	if err := mgr.EnsureWatcher(ctx, 100); err != nil {
		t.Fatalf("EnsureWatcher: %v", err)
	}
	defer mgr.StopWatcher()

	// File inside excluded subdir.
	excluded := filepath.Join(subDir, "dropme.txt")
	if err := os.WriteFile(excluded, []byte("x"), 0o600); err != nil {
		t.Fatalf("write excluded: %v", err)
	}
	// File outside exclude — control sample.
	included := filepath.Join(dir, "keepme.txt")
	if err := os.WriteFile(included, []byte("y"), 0o600); err != nil {
		t.Fatalf("write included: %v", err)
	}

	includedAbs := filepath.ToSlash(included)
	excludedAbs := filepath.ToSlash(excluded)

	// Wait for control sample to land.
	waitFor(t, 3*time.Second, func() bool {
		rows, _ := mgr.ListAll(ctx)
		for _, r := range rows {
			if !r.IsDir && r.RelPath == includedAbs {
				return true
			}
		}
		return false
	})
	// Give a debounce window's worth of slack for the excluded path to
	// also (incorrectly) land if the exclude check failed.
	time.Sleep(400 * time.Millisecond)

	rows, _ := mgr.ListAll(ctx)
	for _, r := range rows {
		if !r.IsDir && r.RelPath == excludedAbs {
			t.Fatalf("excluded file %q leaked into DB", excludedAbs)
		}
	}
}

// TestWatcher_StopIsIdempotent ensures StopWatcher works when the
// watcher was never started AND when called twice. Important because
// the job tick reconciles state every minute and may call Stop on a
// non-running manager (cfg toggled off before watcher ever started).
func TestWatcher_StopIsIdempotent(t *testing.T) {
	db := newDB(t)
	mgr := New(db)

	// Never started — should be a no-op.
	mgr.StopWatcher()

	if err := mgr.EnsureWatcher(context.Background(), 100); err != nil {
		t.Fatalf("EnsureWatcher: %v", err)
	}
	mgr.StopWatcher()
	mgr.StopWatcher() // double-stop must not panic
}
