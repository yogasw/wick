package upgrade

import (
	"os"
	"testing"
	"time"
)

// TestDrainState locks what a draining process tells its successor — and,
// more importantly, when its record must be ignored. A stale file would have
// the UI blame a process that finished minutes ago.
func TestDrainState(t *testing.T) {
	t.Run("a live drain is reported with its work", func(t *testing.T) {
		dir := t.TempDir()
		PublishDrainState(dir, os.Getpid(), time.Now().Add(-20*time.Second), []string{"agent turns=1 (sess-a)"})
		st, ok := ReadDrainState(dir)
		if !ok || st.PID != os.Getpid() || len(st.Outstanding) != 1 {
			t.Fatalf("not reported: %+v %v", st, ok)
		}
	})

	t.Run("a record nobody refreshed is ignored", func(t *testing.T) {
		// The publisher rewrites every few seconds while it drains; silence
		// means it died without cleaning up.
		dir := t.TempDir()
		PublishDrainState(dir, os.Getpid(), time.Now(), []string{"agent turns=1"})
		stale := time.Now().Add(-2 * time.Minute)
		if err := os.Chtimes(drainStatePath(dir), stale, stale); err != nil {
			t.Fatal(err)
		}
		// UpdatedAt lives inside the file, so rewrite it with an old stamp.
		PublishDrainStateAt(dir, os.Getpid(), time.Now(), []string{"agent turns=1"}, stale)
		if _, ok := ReadDrainState(dir); ok {
			t.Fatal("a stale record was reported as a live drain")
		}
	})

	t.Run("a dead pid is ignored", func(t *testing.T) {
		dir := t.TempDir()
		// pid 0 is never a live process here; the reader must not trust the
		// file's own claim about who wrote it.
		PublishDrainState(dir, 999999, time.Now(), []string{"agent turns=1"})
		if _, ok := ReadDrainState(dir); ok {
			t.Fatal("a record from a dead pid was reported")
		}
	})

	t.Run("clearing removes it", func(t *testing.T) {
		dir := t.TempDir()
		PublishDrainState(dir, os.Getpid(), time.Now(), []string{"x"})
		ClearDrainState(dir)
		if _, ok := ReadDrainState(dir); ok {
			t.Fatal("the record survived the drain that wrote it")
		}
	})

	t.Run("no file, no claim", func(t *testing.T) {
		if _, ok := ReadDrainState(t.TempDir()); ok {
			t.Fatal("an absent record produced a verdict")
		}
	})
}
