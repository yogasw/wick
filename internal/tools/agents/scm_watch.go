package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/scm"
	"github.com/yogasw/wick/internal/agents/session"
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
	publishGitSummary(ctx, sessionID, cwd, "")

	// touched is the path of the last file that changed, carried into the
	// debounced publish so the Source panel can follow the repo actually
	// being edited. Guarded because the timer fires on its own goroutine.
	var touchedMu sync.Mutex
	touched := ""

	var timer *time.Timer
	debounce := func() {
		if timer != nil {
			timer.Stop()
		}
		timer = time.AfterFunc(gitWatchDebounce, func() {
			touchedMu.Lock()
			p := touched
			touched = ""
			touchedMu.Unlock()
			publishGitSummary(ctx, sessionID, cwd, p)
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
			if isContentEvent(ev) {
				touchedMu.Lock()
				touched = ev.Name
				touchedMu.Unlock()
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
func publishGitSummary(ctx context.Context, sessionID, cwd, touched string) {
	if globalBcast == nil {
		return
	}
	sess, sessErr := session.Load(globalLayout, sessionID)
	stored := ""
	if sessErr == nil {
		stored = sess.Meta.ScmRepo
	}
	snap := buildGitSnapshot(ctx, cwd, stored)
	// Follow the work: whoever just edited a file — the agent or the
	// person — is working in that repo, so the Source panel moves there
	// instead of sitting on a repo nobody is touching. Without this the
	// panel only ever moved when someone clicked it, which is exactly
	// when it is least needed.
	if sessErr == nil {
		if rel := repoForPath(cwd, snap.Repos, touched); rel != "" && rel != stored {
			meta := sess.Meta
			meta.ScmRepo = rel
			if err := session.SaveMeta(globalLayout, sessionID, meta); err == nil {
				if globalMgr != nil {
					globalMgr.Register(sessionWithMeta(sess, meta))
				}
				snap.Active, snap.ActiveExplicit = rel, true
			}
		}
	}
	body, err := json.Marshal(snap)
	if err != nil {
		return
	}
	globalBcast.PublishGitStatusJSON(sessionID, string(body))
}

// isContentEvent reports whether an fsnotify event is somebody editing
// content, as opposed to git's own bookkeeping. Writes inside .git
// (index, refs, lock files) fire constantly during a commit or a fetch
// and would drag the panel to whichever repo git last touched in the
// background.
func isContentEvent(ev fsnotify.Event) bool {
	if ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename|fsnotify.Remove) == 0 {
		return false
	}
	p := filepath.ToSlash(ev.Name)
	return !strings.Contains(p, "/.git/") && !strings.HasSuffix(p, "/.git")
}

// repoForPath maps an edited file to the repo it belongs to — the
// LONGEST matching repo dir, so a file in a nested clone is attributed to
// the inner repo rather than the outer one. Empty when the path is under
// no repo (loose file in the session cwd).
func repoForPath(cwd string, repos []RepoSummary, path string) string {
	if path == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	best, bestLen := "", -1
	for _, r := range repos {
		dir, derr := scm.ResolveRepoDir(cwd, r.Rel)
		if derr != nil {
			continue
		}
		if abs != dir && !strings.HasPrefix(abs, dir+string(filepath.Separator)) {
			continue
		}
		if len(dir) > bestLen {
			best, bestLen = r.Rel, len(dir)
		}
	}
	return best
}
