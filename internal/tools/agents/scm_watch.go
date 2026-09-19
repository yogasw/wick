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

// The recompute is not cheap and its cost is set by the session directory,
// not by us: it walks the cwd for repos and runs git status in each one. A
// session dir holding 61 clones / ~9800 directories costs well over a
// second of CPU per pass.
//
// The debounce alone does not bound that. It bounds LATENCY after the last
// event, and a build (or an agent writing files in a loop) never stops
// producing events — so the watcher fired every 400ms for the whole build
// and the DAEMON, not the build, pinned a 2-core host: 150-180% CPU, 0%
// idle, with the build sitting politely inside its cgroup quota.
//
// So the pace is tied to what the last pass actually cost. After a
// recompute the watcher waits gitWatchDutyFactor times its duration before
// running another one, which caps the watcher's share of a core at roughly
// 1/(1+factor) — about 20% — no matter how big the directory is or how
// hard something is writing to it. A cheap tree keeps 400ms latency (its
// cooldown lands below the debounce); an expensive one backs itself off to
// seconds, which is exactly when nobody is reading the badge anyway.
const (
	gitWatchDutyFactor  = 4
	gitWatchMaxInterval = 15 * time.Second
)

// gitWatchCooldown is how long to wait after a recompute that took cost.
func gitWatchCooldown(cost time.Duration) time.Duration {
	if cost <= 0 {
		return 0
	}
	if d := cost * gitWatchDutyFactor; d < gitWatchMaxInterval {
		return d
	}
	return gitWatchMaxInterval
}

// gitWatchBudget caps how many directories one session's watcher may
// register. inotify watches are a per-user kernel resource
// (/proc/sys/fs/inotify/max_user_watches, 27916 on this host) and a
// session cwd can hold dozens of clones, so the watcher spends its
// budget on the repo actually being worked in and covers the rest
// shallowly. Exceeding the kernel limit would make Add() fail silently
// for every later directory — worse than a bounded blind spot.
const gitWatchBudget = 4000

// gitWatchManager runs at most one fsnotify watcher per WORKING
// DIRECTORY, ref-counted by the live SSE subscribers attached to it. The
// watcher walks the cwd, recomputes a light repo/changed summary on
// debounced change, and publishes a git_status event to every session
// working in that directory.
//
// Keyed by cwd, not by session, and that distinction is the whole point.
// Sessions of one project SHARE a working directory, so keying by session
// ran one full walk per open session over the same tree: measured here at
// 61 repos / ~9800 directories, where a single pass costs ~1.4 cores. The
// per-watcher duty-cycle cap held, but it capped each watcher
// individually, so three open sessions meant three times the work and the
// daemon sat at 185% CPU while a build wrote into that tree — enough to
// take the node past 70% and get an innocent build killed by the
// resource watchdog.
//
// One walk now serves every session in the project: the expensive part
// (DiscoverRepos + one git status per repo) happens once per pass, and
// only the cheap per-session bits — which repo that session has selected
// — are computed per subscriber.
//
// Lazy lifecycle: acquire starts a watcher on the first subscriber;
// release stops it when the last one goes. This keeps fs watches off
// idle sessions.
type gitWatchManager struct {
	mu      sync.Mutex
	entries map[string]*gitWatchEntry // keyed by cwd
	homes   map[string]string         // session id -> cwd, so release can find its entry
}

type gitWatchEntry struct {
	cancel context.CancelFunc

	// sessions is the subscriber set, with a refcount each: one session
	// can have several tabs open. Guarded by its own lock because the
	// watcher goroutine reads it on every publish.
	mu       sync.Mutex
	sessions map[string]int
}

// attached returns the sessions to publish to, as a snapshot — the
// watcher must not hold the lock while it walks a repository.
func (e *gitWatchEntry) attached() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.sessions))
	for id := range e.sessions {
		out = append(out, id)
	}
	return out
}

var globalGitWatch = &gitWatchManager{
	entries: make(map[string]*gitWatchEntry),
	homes:   make(map[string]string),
}

// acquire attaches sessionID to the watcher for cwd, starting it on the
// first reference.
func (m *gitWatchManager) acquire(sessionID, cwd string) {
	if sessionID == "" || cwd == "" || globalBcast == nil {
		return
	}
	m.mu.Lock()
	e, running := m.entries[cwd]
	if !running {
		e = &gitWatchEntry{sessions: map[string]int{}}
		m.entries[cwd] = e
	}
	m.homes[sessionID] = cwd
	e.mu.Lock()
	e.sessions[sessionID]++
	e.mu.Unlock()
	if !running {
		ctx, cancel := context.WithCancel(context.Background())
		e.cancel = cancel
		go runGitWatch(ctx, cwd, e)
	}
	m.mu.Unlock()

	if running {
		// A watcher is already going, so nothing would publish for this new
		// subscriber until the next filesystem change. Push the current
		// state now — this is the second tab, or a session joining a
		// project someone else is already watching, and it deserves the
		// same correct badge the first one got.
		go publishGitSummary(context.Background(), sessionID, cwd, "")
	}
}

// release detaches sessionID, stopping the watcher when its last
// subscriber goes.
func (m *gitWatchManager) release(sessionID string) {
	if sessionID == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	cwd, ok := m.homes[sessionID]
	if !ok {
		return
	}
	e, ok := m.entries[cwd]
	if !ok {
		delete(m.homes, sessionID)
		return
	}
	e.mu.Lock()
	e.sessions[sessionID]--
	gone := e.sessions[sessionID] <= 0
	if gone {
		delete(e.sessions, sessionID)
	}
	empty := len(e.sessions) == 0
	e.mu.Unlock()

	if gone {
		delete(m.homes, sessionID)
		// Drop this session's replay cache: nobody is watching for it, and
		// the next acquire publishes a fresh snapshot anyway. Keeps the
		// cache bounded by live sessions rather than by every session the
		// process has ever seen.
		if globalBcast != nil {
			globalBcast.ForgetGitStatus(sessionID)
		}
	}
	if empty {
		if e.cancel != nil {
			e.cancel()
		}
		delete(m.entries, cwd)
	}
}

// runGitWatch watches cwd recursively (one level of repos deep) and
// publishes a git_status summary on debounced changes until ctx is done.
func runGitWatch(ctx context.Context, cwd string, entry *gitWatchEntry) {
	l := log.With().Str("component", "scm-watch").Str("cwd", cwd).Logger()
	w, err := fsnotify.NewWatcher()
	if err != nil {
		l.Warn().Err(err).Msg("scm-watch: new watcher failed")
		return
	}
	defer w.Close()

	// The active repo decides where the deep watches go, so read it before
	// registering anything. With several sessions on one directory the
	// first one's selection seeds it; a later publish moves the deep
	// watches if the work turns out to be elsewhere.
	active := ""
	for _, id := range entry.attached() {
		if active = storedActiveRepo(id); active != "" {
			break
		}
	}
	budget := addWatches(w, cwd, active, &l)
	// Publish an initial summary so every attached badge is correct on
	// connect. One snapshot, reused per session.
	publishGitSummaryTo(ctx, entry.attached(), cwd, "")

	// touched is the path of the last file that changed, carried into the
	// debounced publish so the Source panel can follow the repo actually
	// being edited. Guarded because the timer fires on its own goroutine.
	var touchedMu sync.Mutex
	touched := ""

	// When the last recompute finished, and what it cost — the two numbers
	// the pace is derived from. Written by the timer goroutine, read by the
	// event loop, so they live under their own lock.
	var paceMu sync.Mutex
	var lastEnd time.Time
	var lastCost time.Duration

	var timer *time.Timer
	debounce := func() {
		if timer != nil {
			timer.Stop()
		}
		wait := gitWatchDebounce
		paceMu.Lock()
		if !lastEnd.IsZero() {
			// Still inside the previous pass's cooldown: fire when it ends
			// rather than now. The event that arrived is not lost — the
			// timer is reset on every event, so the publish that eventually
			// runs reflects the newest state.
			if left := time.Until(lastEnd.Add(gitWatchCooldown(lastCost))); left > wait {
				wait = left
			}
		}
		paceMu.Unlock()
		timer = time.AfterFunc(wait, func() {
			touchedMu.Lock()
			p := touched
			touched = ""
			touchedMu.Unlock()
			started := time.Now()
			now := publishGitSummaryTo(ctx, entry.attached(), cwd, p)
			paceMu.Lock()
			lastCost = time.Since(started)
			lastEnd = time.Now()
			paceMu.Unlock()
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
	return publishGitSummaryTo(ctx, []string{sessionID}, cwd, touched)
}

// publishGitSummaryTo walks the tree ONCE and publishes to every session
// working in it.
//
// The walk — DiscoverRepos plus a git status per repo — is the expensive
// half and does not depend on who is watching, so it must not be repeated
// per subscriber. Only the tail is per session: which repo that session
// has selected, and whether the edit just made should move that selection.
// Doing this per session was what made one project with several open
// sessions cost several full walks of the same directory.
func publishGitSummaryTo(ctx context.Context, sessionIDs []string, cwd, touched string) string {
	if globalBcast == nil || len(sessionIDs) == 0 {
		return ""
	}
	base := buildGitSnapshot(ctx, cwd, "")
	first := ""
	for _, sessionID := range sessionIDs {
		active := publishGitSnapshot(sessionID, cwd, touched, base)
		if first == "" {
			first = active
		}
	}
	return first
}

// publishGitSnapshot applies one session's selection to a shared snapshot
// and sends it. Returns the repo that session is now active in.
func publishGitSnapshot(sessionID, cwd, touched string, base GitStatusSnapshot) string {
	sess, sessErr := session.Load(globalLayout, sessionID)
	stored := ""
	if sessErr == nil {
		stored = sess.Meta.ScmRepo
	}
	// Copy the shared snapshot: the repo list is identical for everyone,
	// but Active is this session's own answer and must not be written into
	// a value other sessions are about to read.
	snap := base
	if sel, err := scm.ResolveSelection(cwd, stored); err == nil {
		snap.Active, snap.ActiveExplicit = sel.Rel, sel.Explicit
	}
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
