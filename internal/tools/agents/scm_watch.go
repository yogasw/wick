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

// gitWatchBudget caps how many directories one session's watcher may
// register. inotify watches are a per-user kernel resource
// (/proc/sys/fs/inotify/max_user_watches, 27916 on this host) and a
// session cwd can hold dozens of clones, so the watcher spends its
// budget on the repo actually being worked in and covers the rest
// shallowly. Exceeding the kernel limit would make Add() fail silently
// for every later directory — worse than a bounded blind spot.
const gitWatchBudget = 4000

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
		// A watcher is already running, so nothing would publish for this
		// new subscriber until the next filesystem change. Push the current
		// state now — this is the second tab / re-subscribing page, and it
		// deserves the same correct badge the first one got.
		go publishGitSummary(context.Background(), sessionID, cwd, "")
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
		// Drop the replay cache with the watcher: nobody is watching, and
		// the next acquire publishes a fresh snapshot anyway. Keeps the
		// cache bounded by live sessions rather than by every session the
		// process has ever seen.
		if globalBcast != nil {
			globalBcast.ForgetGitStatus(sessionID)
		}
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

	// The active repo decides where the deep watches go, so read it before
	// registering anything.
	active := storedActiveRepo(sessionID)
	budget := addWatches(w, cwd, active, &l)
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
			now := publishGitSummary(ctx, sessionID, cwd, p)
			// Work moved to another repo (the panel followed an edit, or
			// somebody picked a different source). Give THAT repo the deep
			// watches, or the blind spot just moves with it.
			touchedMu.Lock()
			changed := now != "" && now != active
			if changed {
				active = now
			}
			cur := active
			touchedMu.Unlock()
			if changed {
				addRecursive(w, cwd, cur, budget, &l)
			}
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

// watchBudget hands out a bounded number of inotify watches.
type watchBudget struct {
	used int
	max  int
}

// add registers dir unless the budget is spent. Reports whether there is
// room for more, so callers can stop walking instead of hammering a
// watcher that will refuse everything.
func (b *watchBudget) add(w *fsnotify.Watcher, dir string) bool {
	if b.used >= b.max {
		return false
	}
	if err := w.Add(dir); err == nil {
		b.used++
	}
	return b.used < b.max
}

// addWatches registers the directories whose changes must wake the Source
// panel: the session cwd, every repo root (plus its .git, so a commit or a
// `git add` from a terminal refreshes too), and then — this is the part that
// matters — the ACTIVE repo's whole tree.
//
// fsnotify is not recursive, so a directory that was never added produces no
// events at all. The previous version added only the repo root and ONE level
// below it, which meant an edit to app/app.go was noticed and an edit to
// internal/pkg/api/server.go was not: the panel stayed stale until someone
// reloaded the page. Since the FE renders purely from the git_status event
// (no polling), a missing watch reads to the user as "wick doesn't see my
// changes".
//
// Order is deliberate: the active repo is walked first so it always gets full
// coverage even when the budget runs out on a cwd holding 50+ clones.
func addWatches(w *fsnotify.Watcher, root, active string, l *zerolog.Logger) *watchBudget {
	b := &watchBudget{max: gitWatchBudget}
	b.add(w, root)

	repos, err := scm.DiscoverRepos(root)
	if err != nil {
		return b
	}
	for _, r := range repos {
		dir := watchDirFor(root, r.Rel)
		if !b.add(w, dir) {
			break
		}
		// .git itself, non-recursively: index / HEAD / refs writes are how
		// staging and committing outside the UI announce themselves.
		b.add(w, filepath.Join(dir, ".git"))
		addShallow(w, dir, b)
	}
	addRecursive(w, root, active, b, l)
	return b
}

// addRecursive walks one repo's working tree and watches every directory in
// it, within budget. Skips .git and the usual build/cache noise so a
// node_modules tree cannot eat the whole budget.
func addRecursive(w *fsnotify.Watcher, root, rel string, b *watchBudget, l *zerolog.Logger) {
	if rel == "" {
		return
	}
	dir := watchDirFor(root, rel)
	before := b.used
	room := true
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil //nolint:nilerr // an unreadable subtree is skipped, not fatal
		}
		name := d.Name()
		if p != dir && (name == ".git" || skipWatchDir(name)) {
			return filepath.SkipDir
		}
		room = b.add(w, p)
		if !room {
			return filepath.SkipAll
		}
		return nil
	})
	if l != nil {
		ev := l.Debug()
		if !room {
			ev = l.Warn()
		}
		ev.Str("repo", rel).Int("dirs", b.used-before).Int("used", b.used).
			Bool("budget_exhausted", !room).Msg("scm-watch: watching repo tree")
	}
}

// addShallow adds the immediate subdirectories of dir (skipping noise +
// .git) so edits in common source folders fire events even in repos the
// recursive walk did not reach.
func addShallow(w *fsnotify.Watcher, dir string, b *watchBudget) {
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
		if !b.add(w, filepath.Join(dir, name)) {
			return
		}
	}
}

// watchDirFor resolves a repo's rel path (as DiscoverRepos reports it) to an
// absolute directory. "." is the cwd itself. Named apart from scm.go's
// repoDir, which resolves a request's repo from a tool context.
func watchDirFor(root, rel string) string {
	if rel == "" || rel == "." {
		return root
	}
	return filepath.Join(root, filepath.FromSlash(rel))
}

func skipWatchDir(name string) bool {
	switch name {
	case "node_modules", "vendor", "dist", "build", ".next", ".cache", "target",
		".venv", "__pycache__", ".codegraph", ".svelte-kit", ".terraform",
		".pytest_cache", ".mypy_cache", ".ruff_cache", ".gradle":
		return true
	}
	return false
}

// publishGitSummary recomputes the FULL snapshot for cwd (repos +
// per-repo status) and pushes it to the session's subscribers. The FE
// renders entirely from this payload, so a change event needs no
// follow-up fetch — zero polling.
// storedActiveRepo reads the session's selected repo, or "" when there is
// none (or the session cannot be read).
func storedActiveRepo(sessionID string) string {
	sess, err := session.Load(globalLayout, sessionID)
	if err != nil {
		return ""
	}
	return sess.Meta.ScmRepo
}

// publishGitSummary recomputes and pushes the snapshot, and reports which
// repo ended up active so the caller can extend its watches to it.
func publishGitSummary(ctx context.Context, sessionID, cwd, touched string) string {
	if globalBcast == nil {
		return ""
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
		return snap.Active
	}
	globalBcast.PublishGitStatusJSON(sessionID, string(body))
	return snap.Active
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
