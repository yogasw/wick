package agents

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog"

	agentsconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
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

// The watcher's pace has to come from what a recompute COSTS, not from a
// fixed number: the same 400ms debounce that is fine over one small repo is
// what let a 61-repo session dir pin a 2-core host while a build wrote
// files continuously.
func TestGitWatchCooldownScalesWithCost(t *testing.T) {
	// Nothing measured yet (first pass): no cooldown, so the badge is
	// correct as fast as the debounce allows.
	if got := gitWatchCooldown(0); got != 0 {
		t.Errorf("cooldown(0) = %v, want none", got)
	}

	// A cheap tree stays effectively unthrottled — its cooldown lands below
	// the debounce, so latency is unchanged.
	if got := gitWatchCooldown(20 * time.Millisecond); got >= gitWatchDebounce {
		t.Errorf("cooldown(20ms) = %v, want below the %v debounce", got, gitWatchDebounce)
	}

	// An expensive one backs itself off: 4x the cost means the walk can
	// occupy at most ~1/5 of the time, whatever the directory holds.
	if got := gitWatchCooldown(1500 * time.Millisecond); got != 6*time.Second {
		t.Errorf("cooldown(1.5s) = %v, want 6s (4x)", got)
	}

	// And it is capped, so a pathological pass cannot silence the panel for
	// minutes.
	if got := gitWatchCooldown(time.Minute); got != gitWatchMaxInterval {
		t.Errorf("cooldown(1m) = %v, want the %v ceiling", got, gitWatchMaxInterval)
	}
}

// ── one watcher per directory, not per session ────────────────────────

// Sessions of one project share a working directory. Keying the watcher
// by session ran a full walk of that directory PER SESSION — 61 repos and
// ~9800 directories each, at roughly 1.4 cores a pass. The per-watcher
// duty-cycle cap was working; it just capped each copy, so three open
// sessions cost three times the work and the daemon sat at 185% CPU while
// a build wrote into the tree.
func TestOneWatcherPerDirectory(t *testing.T) {
	m := &gitWatchManager{
		entries: make(map[string]*gitWatchEntry),
	}
	cwd := t.TempDir()

	// Attach three sessions to the same directory WITHOUT starting a real
	// watcher: the entry bookkeeping is what this pins.
	for _, id := range []string{"s1", "s2", "s3"} {
		m.mu.Lock()
		e, ok := m.entries[cwd]
		if !ok {
			e = &gitWatchEntry{sessions: map[string]int{}}
			m.entries[cwd] = e
		}
		e.mu.Lock()
		e.sessions[id]++
		e.mu.Unlock()
		m.mu.Unlock()
	}

	if len(m.entries) != 1 {
		t.Fatalf("%d watchers for one directory, want exactly 1", len(m.entries))
	}
	if got := len(m.entries[cwd].attached()); got != 3 {
		t.Errorf("%d sessions attached, want 3", got)
	}

	// The watcher survives until its LAST subscriber leaves, otherwise one
	// person closing a tab would blind everyone else in the project.
	m.release("s1", cwd)
	if len(m.entries) != 1 {
		t.Fatalf("the watcher stopped while two sessions were still attached")
	}
	m.release("s2", cwd)
	if len(m.entries) != 1 {
		t.Fatalf("the watcher stopped while a session was still attached")
	}
	m.release("s3", cwd)
	if len(m.entries) != 0 {
		t.Errorf("the watcher outlived its last subscriber: %v", m.entries)
	}
}

// Two tabs of one session are two references, not two watchers — and
// closing one must not detach the session.
func TestSecondTabIsAReferenceNotAWatcher(t *testing.T) {
	m := &gitWatchManager{
		entries: make(map[string]*gitWatchEntry),
	}
	cwd := t.TempDir()
	e := &gitWatchEntry{sessions: map[string]int{"s1": 2}}
	m.entries[cwd] = e

	m.release("s1", cwd)
	if len(m.entries) != 1 || len(e.attached()) != 1 {
		t.Fatalf("closing one tab detached the session: entries=%d attached=%v", len(m.entries), e.attached())
	}
	m.release("s1", cwd)
	if len(m.entries) != 0 {
		t.Errorf("the last tab left the watcher running")
	}
}

// Releasing something never acquired must be a no-op rather than a panic:
// it happens whenever a subscriber disconnects after a restart.
func TestReleaseUnknownSessionIsHarmless(t *testing.T) {
	m := &gitWatchManager{
		entries: make(map[string]*gitWatchEntry),
	}
	m.release("never-seen", t.TempDir())
	m.release("", "")
}

// A session moved to another project while its stream stays open ends up
// referenced from TWO directories: the old stream still holds the old
// project, the next one acquires the new. Each release has to drop the
// reference it took. Looking the directory up by session id let the
// second acquire overwrite the first, so the old stream's release hit the
// NEW project and the old project's watcher stayed attached for good —
// pushing the old project's repos into the moved session.
func TestReleaseAfterMoveDropsEachProjectsReference(t *testing.T) {
	prevBcast := globalBcast
	t.Cleanup(func() { globalBcast = prevBcast })
	globalBcast = NewBroadcaster()
	globalBcast.PublishGitStatusJSON("s1", `{"repos":[],"active":""}`)

	m := &gitWatchManager{
		entries: make(map[string]*gitWatchEntry),
	}
	oldCwd, newCwd := t.TempDir(), t.TempDir()
	m.entries[oldCwd] = &gitWatchEntry{sessions: map[string]int{"s1": 1}}
	m.entries[newCwd] = &gitWatchEntry{sessions: map[string]int{"s1": 1, "s2": 1}}

	// The stream opened before the move closes first.
	m.release("s1", oldCwd)
	if _, ok := m.entries[oldCwd]; ok {
		t.Fatalf("the old project's watcher is still attached after its stream closed")
	}
	if got := m.entries[newCwd].attached(); len(got) != 2 {
		t.Fatalf("closing the old stream detached sessions from the new project: %v", got)
	}
	// The new project's stream still replays this session's snapshot.
	if _, ok := globalBcast.LastGitStatus("s1"); !ok {
		t.Fatalf("closing the old stream dropped the replay cache the new project's stream uses")
	}

	// A release for a directory the session never joined changes nothing.
	m.release("s1", oldCwd)
	if got := m.entries[newCwd].attached(); len(got) != 2 {
		t.Fatalf("a stray release detached sessions: %v", got)
	}

	// Its last reference anywhere gone, the cache goes too.
	m.release("s1", newCwd)
	if _, ok := globalBcast.LastGitStatus("s1"); ok {
		t.Errorf("replay cache outlived the session's last watcher")
	}
}

// Until its stream reconnects, a moved session is still attached to the
// old project's watcher. That watcher must neither publish the old tree to
// it nor let an edit there move the session's selection: the result was a
// Source panel and an active_repo naming a repo outside the session's own
// folder.
func TestMovedSessionIgnoresTheOldProjectsWatcher(t *testing.T) {
	layout := agentsconfig.NewLayout(t.TempDir())
	if err := layout.EnsureLayout(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"old", "new"} {
		if _, err := project.Create(layout, project.CreateOptions{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := session.Create(context.Background(), layout, session.CreateOptions{
		ID: "s1", ProjectID: "old", Origin: session.OriginUI,
	}); err != nil {
		t.Fatal(err)
	}
	if err := session.SetProject(context.Background(), layout, "s1", "new"); err != nil {
		t.Fatal(err)
	}

	prevLayout, prevBcast, prevMgr := globalLayout, globalBcast, globalMgr
	t.Cleanup(func() { globalLayout, globalBcast, globalMgr = prevLayout, prevBcast, prevMgr })
	globalLayout, globalBcast, globalMgr = layout, NewBroadcaster(), nil

	repoIn := func(projectID, rel string) (string, GitStatusSnapshot) {
		cwd, err := project.ResolvePath(layout, projectID)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(cwd, rel, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		return cwd, GitStatusSnapshot{Repos: []RepoSummary{{Rel: rel}}}
	}

	oldCwd, oldSnap := repoIn("old", "old-repo")
	publishGitSnapshot("s1", oldCwd, filepath.Join(oldCwd, "old-repo", "main.go"), oldSnap)
	if body, ok := globalBcast.LastGitStatus("s1"); ok {
		t.Errorf("the old project's tree was published to the moved session: %s", body)
	}
	sess, err := session.Load(layout, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Meta.ScmRepo != "" {
		t.Errorf("an edit in the old project selected %q for the moved session", sess.Meta.ScmRepo)
	}

	// Control: the session's own project still publishes and follows edits.
	newCwd, newSnap := repoIn("new", "new-repo")
	publishGitSnapshot("s1", newCwd, filepath.Join(newCwd, "new-repo", "main.go"), newSnap)
	if _, ok := globalBcast.LastGitStatus("s1"); !ok {
		t.Errorf("the session's own project published nothing")
	}
	if sess, _ = session.Load(layout, "s1"); sess.Meta.ScmRepo != "new-repo" {
		t.Errorf("selection = %q, want new-repo", sess.Meta.ScmRepo)
	}
}

// Leaving the old project's watcher must not wipe the replay cache the
// moved session's new-project stream still serves from; only the last
// reference anywhere drops it.
func TestReleaseKeepsCacheWhileAttachedElsewhere(t *testing.T) {
	prevBcast := globalBcast
	t.Cleanup(func() { globalBcast = prevBcast })
	globalBcast = NewBroadcaster()

	m := &gitWatchManager{entries: make(map[string]*gitWatchEntry)}
	oldCwd, newCwd := t.TempDir(), t.TempDir()
	m.entries[oldCwd] = &gitWatchEntry{sessions: map[string]int{"s1": 1}}
	m.entries[newCwd] = &gitWatchEntry{sessions: map[string]int{"s1": 1}}
	globalBcast.PublishGitStatusJSON("s1", `{"repos":[{"rel":"new-repo"}],"active":"new-repo"}`)

	m.release("s1", oldCwd)
	if _, ok := globalBcast.LastGitStatus("s1"); !ok {
		t.Fatalf("leaving the old project dropped the cache the new project's stream needs")
	}
	m.release("s1", newCwd)
	if _, ok := globalBcast.LastGitStatus("s1"); ok {
		t.Fatalf("cache kept after the session's last watcher reference went")
	}
}
