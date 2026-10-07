package daemon

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/yogasw/wick/internal/pkg/upgrade"
)

func writeState(t *testing.T, dir string, at time.Time, ok bool, msg string) {
	t.Helper()
	b, err := json.Marshal(map[string]any{"at": at, "ok": ok, "error": msg})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(upgrade.HandoverStatePath(dir), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRefusedSince locks the rule that turns a five-minute wait into a
// one-line answer — and the rule that stops an OLD refusal from killing a
// reload that is going fine.
func TestRefusedSince(t *testing.T) {
	t.Run("a refusal recorded during this reload aborts it", func(t *testing.T) {
		dir := t.TempDir()
		start := time.Now()
		writeState(t, dir, start.Add(2*time.Second), false, "parent hasn't exited")
		why, ok := refusedSince(dir, start)
		if !ok || why != "parent hasn't exited" {
			t.Fatalf("refusal not surfaced: %q %v", why, ok)
		}
	})

	t.Run("a refusal from a PREVIOUS reload is ignored", func(t *testing.T) {
		// Otherwise every later reload dies instantly on a stale file, which
		// is a worse failure than the one this fixes: it never even tries.
		dir := t.TempDir()
		start := time.Now()
		writeState(t, dir, start.Add(-10*time.Minute), false, "old news")
		if _, ok := refusedSince(dir, start); ok {
			t.Fatal("a stale refusal aborted a fresh reload")
		}
	})

	t.Run("a successful handover is not a refusal", func(t *testing.T) {
		dir := t.TempDir()
		start := time.Now()
		writeState(t, dir, start.Add(time.Second), true, "")
		if _, ok := refusedSince(dir, start); ok {
			t.Fatal("a success was read as a refusal")
		}
	})

	t.Run("no file, no opinion", func(t *testing.T) {
		if _, ok := refusedSince(t.TempDir(), time.Now()); ok {
			t.Fatal("an absent record produced a verdict")
		}
	})

	t.Run("an empty dir is not consulted", func(t *testing.T) {
		if _, ok := refusedSince("", time.Now()); ok {
			t.Fatal("empty dir produced a verdict")
		}
	})
}
