package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/scm"
)

// gitWatchDebounce coalesces bursty filesystem events (a single git
// operation touches many files) into one git_status recompute.
const gitWatchDebounce = 400 * time.Millisecond

// gitWatchManager runs at most one fsnotify watcher per session cwd,
// ref-counted by the number of live SSE subscribers. The watcher walks
// the cwd, recomputes a light repo/changed summary on debounced change,
// and publishes a git_status event to the session's subscribers.
//
// Lazy lifecycle: acquireGitWatch starts a watcher on the first
// subscriber; releaseGitWatch stops it when the count hits zero. This
// keeps fs watches off idle sessions.
type gitWatchManager struct {
	mu      sync.Mutex
	entries map[string]*gitWatchEntry // keyed by session id
}

type gitWatchEntry struct {
	refs   int
	cancel context.CancelFunc
}

var globalGitWatch = &gitWatchManager{entries: make(map[string]*gitWatchEntry)}

// acquireGitWatch increments the watcher refcount for sessionID, starting
// the watcher (rooted at cwd) on the first reference.
func (m *gitWatchManager) acquire(sessionID, cwd string) {
	if sessionID == "" || cwd == "" || globalBcast == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.entries[sessionID]; ok {
		e.refs++
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.entries[sessionID] = &gitWatchEntry{refs: 1, cancel: cancel}
	go runGitWatch(ctx, sessionID, cwd)
}

// releaseGitWatch decrements the refcount, stopping the watcher at zero.
func (m *gitWatchManager) release(sessionID string) {
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[sessionID]
	if !ok {
		return
	}
	e.refs--
	if e.refs <= 0 {
		e.cancel()
		delete(m.entries, sessionID)
	}
}

// runGitWatch watches cwd recursively (one level of repos deep) and
// publishes a git_status summary on debounced changes until ctx is done.
func runGitWatch(ctx context.Context, sessionID, cwd string) {
	l := log.With().Str("component", "scm-watch").Str("session", sessionID).Logger()
	w, err := fsnotify.NewWatcher()
	if err != nil {
		l.Warn().Err(err).Msg("scm-watch: new watcher failed")
		return
	}
	defer w.Close()

	addWatches(w, cwd, &l)
	// Publish an initial summary so the badge is correct on connect.
	publishGitSummary(ctx, sessionID, cwd)

	var timer *time.Timer
	debounce := func() {
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(gitWatchDebounce, func() {
			publishGitSummary(ctx, sessionID, cwd)
		})
	}

	for {
		select {
		case <-ctx.Done():
			if timer != nil {
				timer.Stop()
			}
			return
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			// New dir created → start watching it too (shallow).
			if ev.Op&fsnotify.Create != 0 {
				if fi, statErr := os.Stat(ev.Name); statErr == nil && fi.IsDir() {
					_ = w.Add(ev.Name)
				}
			}
			debounce()
		case werr, ok := <-w.Errors:
			if !ok {
				return
			}
			l.Debug().Err(werr).Msg("scm-watch: watcher error")
		}
	}
}

// addWatches registers cwd and its subdirectories (bounded) with the
// watcher. fsnotify is non-recursive, so we add each dir; .git internal
// churn is noise but harmless (debounced) — skip heavy noise dirs.
func addWatches(w *fsnotify.Watcher, root string, l *zerolog.Logger) {
	_ = w.Add(root)
	repos, err := scm.DiscoverRepos(root)
	if err != nil {
		return
	}
	for _, r := range repos {
		dir := root
		if r.Rel != "." {
			dir = filepath.Join(root, filepath.FromSlash(r.Rel))
		}
		_ = w.Add(dir)
		// Watch the repo's working tree shallowly (one level) so most
		// edits register without descending the entire tree.
		addShallow(w, dir)
	}
}

// addShallow adds the immediate subdirectories of dir (skipping noise +
// .git) so edits in common source folders fire events without a full
// recursive watch.
func addShallow(w *fsnotify.Watcher, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == ".git" || skipWatchDir(name) {
			continue
		}
		_ = w.Add(filepath.Join(dir, name))
	}
}

func skipWatchDir(name string) bool {
	switch name {
	case "node_modules", "vendor", "dist", "build", ".next", ".cache", "target", ".venv", "__pycache__":
		return true
	}
	return false
}

// publishGitSummary recomputes the FULL snapshot for cwd (repos +
// per-repo status) and pushes it to the session's subscribers. The FE
// renders entirely from this payload, so a change event needs no
// follow-up fetch — zero polling.
func publishGitSummary(ctx context.Context, sessionID, cwd string) {
	if globalBcast == nil {
		return
	}
	snap := buildGitSnapshot(ctx, cwd)
	body, err := json.Marshal(snap)
	if err != nil {
		return
	}
	globalBcast.PublishGitStatusJSON(sessionID, string(body))
}
