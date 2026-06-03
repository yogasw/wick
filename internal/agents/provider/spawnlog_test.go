package provider

import (
	"testing"
	"time"
)

// TestSpawnLogPruneKeepsNewest writes more than the cap and asserts Prune
// keeps only the newest N files (oldest deleted).
func TestSpawnLogPruneKeepsNewest(t *testing.T) {
	sl := &SpawnLogger{BaseDir: t.TempDir()}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// 60 spawns, increasing start time (file 0 oldest … 59 newest).
	for i := 0; i < 60; i++ {
		path := sl.Path("claude", "claude", "sess", base.Add(time.Duration(i)*time.Minute))
		if err := sl.Append(path, SpawnEvent{Type: "start", At: base}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	// Append already prunes to MaxSpawnLogs (50) on each start event.
	got, err := sl.List("", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != MaxSpawnLogs {
		t.Fatalf("expected %d files after auto-prune, got %d", MaxSpawnLogs, len(got))
	}
	// Newest first; the most recent start time must survive.
	want := base.Add(59 * time.Minute)
	if !got[0].StartedAt.Equal(want) {
		t.Fatalf("newest survivor = %v, want %v", got[0].StartedAt, want)
	}

	// Explicit Prune to a smaller cap.
	if err := sl.Prune(10); err != nil {
		t.Fatal(err)
	}
	got, _ = sl.List("", "", "")
	if len(got) != 10 {
		t.Fatalf("after Prune(10): expected 10, got %d", len(got))
	}
}
