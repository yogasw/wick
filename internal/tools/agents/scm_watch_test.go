package agents

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog"
)

// The Source panel renders only from the git_status event, and that event is
// published from filesystem notifications. fsnotify is not recursive, so a
// directory nobody watched produces no event and the panel silently stays
// stale — which is what "wick doesn't see my changes, I have to reload"
// actually was. This test pins the depth.
func TestAddWatchesCoversDeepDirsOfActiveRepo(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "wick")
	deep := filepath.Join(repo, "internal", "pkg", "api", "handlers")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Noise that must NOT be watched (it would eat the budget).
	if err := os.MkdirAll(filepath.Join(repo, "node_modules", "left-pad"), 0o755); err != nil {
		t.Fatal(err)
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Skipf("no watcher available: %v", err)
	}
	defer w.Close()

	l := zerolog.Nop()
	addWatches(w, root, "wick", &l)
	got := w.WatchList()

	for _, want := range []string{
		root,
		repo,
		filepath.Join(repo, ".git"),
		filepath.Join(repo, "internal"),
		filepath.Join(repo, "internal", "pkg"),
		filepath.Join(repo, "internal", "pkg", "api"),
		deep,
	} {
		if !slices.Contains(got, want) {
			t.Errorf("directory not watched: %s\nwatching: %v", want, got)
		}
	}
	if slices.Contains(got, filepath.Join(repo, "node_modules")) {
		t.Errorf("node_modules should be skipped, watching: %v", got)
	}
}

// The budget exists because inotify watches are a per-user kernel resource
// and a session cwd can hold dozens of clones. Exhausting it must stop the
// walk, not keep calling Add for every remaining directory.
func TestWatchBudgetStopsAdding(t *testing.T) {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		t.Skipf("no watcher available: %v", err)
	}
	defer w.Close()

	dir := t.TempDir()
	b := &watchBudget{max: 1}
	if room := b.add(w, dir); room {
		t.Fatalf("budget of 1 should report no room after the first add")
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	b.add(w, sub)
	if slices.Contains(w.WatchList(), sub) {
		t.Errorf("add past the budget should be a no-op, watching: %v", w.WatchList())
	}
	if b.used != 1 {
		t.Errorf("used = %d, want 1", b.used)
	}
}

// The Source rail badge is rendered ONLY from git_status events — there is no
// REST fallback for it. The SharedWorker keeps one stream alive across page
// loads, so a reloaded page never re-opens the stream and instead asks for a
// snapshot; if that snapshot omits git_status, the badge shows nothing until
// the next filesystem change. This pins the replay.
func TestSnapshotEventsReplaysGitStatus(t *testing.T) {
	prevBcast, prevPool := globalBcast, globalPool
	t.Cleanup(func() { globalBcast, globalPool = prevBcast, prevPool })

	globalBcast = NewBroadcaster()
	globalPool = nil // no live agent: git status must still be replayed

	const sid = "slack-owner-123.456"
	if evs := snapshotEvents(sid); len(evs) != 0 {
		t.Fatalf("expected no events before any publish, got %d", len(evs))
	}

	globalBcast.PublishGitStatusJSON(sid, `{"repos":[{"rel":"wick","changed":35}],"active":"wick"}`)
	evs := snapshotEvents(sid)
	if len(evs) != 1 || evs[0].Type != "git_status" {
		t.Fatalf("git_status not replayed, got %#v", evs)
	}
	if evs[0].Data != `{"repos":[{"rel":"wick","changed":35}],"active":"wick"}` {
		t.Errorf("payload altered: %s", evs[0].Data)
	}

	// The cache is dropped when the last watcher goes away, so it stays
	// bounded by live sessions rather than every session ever seen.
	globalBcast.ForgetGitStatus(sid)
	if evs := snapshotEvents(sid); len(evs) != 0 {
		t.Errorf("cache should be empty after ForgetGitStatus, got %#v", evs)
	}
}
