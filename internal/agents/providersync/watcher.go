package providersync

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/entity"
)

// watcher mirrors Configured Sources onto a fsnotify watcher so that file
// changes propagate to DB in near-realtime (sub-debounce-window latency)
// instead of waiting for the cron tick. Lifecycle is owned by Manager —
// see Manager.EnsureWatcher / StopWatcher / Reload.
//
// Why not just rely on the cron tick? With ~6k files the polling pass
// stat()s + hashes every entry every minute, which on small containers
// has triggered OOM and is wasteful when idle. The watcher does zero work
// when nothing changes; the cron is retained as a safety net for events
// the kernel drops (overflow) or platforms that don't emit (some network
// mounts).
//
// Coalescing strategy: events go into a per-path map keyed by absolute
// path. A flush ticker pops entries that have been "quiet" for at least
// the debounce window and runs SyncFile on each. Editor save bursts
// (atomic rename + multiple writes) thus collapse into a single sync.
type watcher struct {
	mgr      *Manager
	fsw      *fsnotify.Watcher
	debounce time.Duration

	mu       sync.Mutex
	watched  map[string]bool                       // abs (slash) dir → registered with kernel
	pending  map[string]time.Time                  // abs (slash) file → last event ts
	excludes []string                              // current exclude patterns
	sources  []entity.ProviderStorageSource        // snapshot of enabled+exclude rows for event-time lookup

	stopCh chan struct{}
	wg     sync.WaitGroup
	l      zerolog.Logger
}

// newWatcher creates a watcher but does NOT start it. Caller must call
// start() afterward with the current source list.
func newWatcher(mgr *Manager, debounce time.Duration) (*watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if debounce <= 0 {
		debounce = time.Second
	}
	return &watcher{
		mgr:      mgr,
		fsw:      fsw,
		debounce: debounce,
		watched:  map[string]bool{},
		pending:  map[string]time.Time{},
		stopCh:   make(chan struct{}),
		l:        log.With().Str("component", "provider-storage").Str("op", "watch").Logger(),
	}, nil
}

// start registers initial dirs and launches the event + flush loops.
func (w *watcher) start(ctx context.Context, sources []entity.ProviderStorageSource) error {
	if err := w.sync(ctx, sources); err != nil {
		return err
	}
	w.wg.Add(2)
	go w.eventLoop(ctx)
	go w.flushLoop(ctx)
	w.l.Info().Int("dirs", len(w.watched)).Dur("debounce", w.debounce).Msg("watcher: started")
	return nil
}

// stop closes the fsnotify watcher and waits for goroutines to drain.
func (w *watcher) stop() {
	close(w.stopCh)
	_ = w.fsw.Close()
	w.wg.Wait()
	w.l.Info().Msg("watcher: stopped")
}

// sync reconciles the kernel-registered dir set with the configured
// sources. Called on start and on every Manager.Reload (which fires
// after SaveSource/DeleteSource so toggling a row in the UI takes
// effect immediately without a job tick).
//
// Folder mode: walks the subtree and registers every non-excluded dir
// (fsnotify is not recursive on Linux/Windows). Single mode: registers
// the parent dir; the event handler filters by exact path.
func (w *watcher) sync(ctx context.Context, sources []entity.ProviderStorageSource) error {
	want := map[string]bool{}
	excludes := collectExcludePatterns(sources)

	addDir := func(abs string) {
		if matchesAnyExclude(abs, excludes) {
			return
		}
		want[abs] = true
	}

	for _, s := range sources {
		if !s.Enabled || s.Mode == "exclude" {
			continue
		}
		base := filepath.Clean(s.SyncPath)
		switch s.Mode {
		case "single":
			parent := filepath.Dir(base)
			if parent != "" && parent != "." {
				addDir(filepath.ToSlash(parent))
			}
		case "folder":
			_ = filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if !d.IsDir() {
					return nil
				}
				abs := filepath.ToSlash(p)
				if matchesAnyExclude(abs, excludes) {
					return filepath.SkipDir
				}
				want[abs] = true
				return nil
			})
		}
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	added, removed := 0, 0
	for p := range w.watched {
		if !want[p] {
			_ = w.fsw.Remove(filepath.FromSlash(p))
			delete(w.watched, p)
			removed++
		}
	}
	for p := range want {
		if w.watched[p] {
			continue
		}
		if err := w.fsw.Add(filepath.FromSlash(p)); err != nil {
			// Common cause on Linux: fs.inotify.max_user_watches exhausted.
			// Log once per Add — caller can decide whether to ramp the limit.
			w.l.Debug().Err(err).Str("path", p).Msg("watcher: Add failed")
			continue
		}
		w.watched[p] = true
		added++
	}
	w.excludes = excludes
	w.sources = sources

	if added > 0 || removed > 0 {
		w.l.Info().Int("added", added).Int("removed", removed).Int("total", len(w.watched)).Msg("watcher: reload")
	}
	return nil
}

func (w *watcher) eventLoop(ctx context.Context) {
	defer w.wg.Done()
	for {
		select {
		case <-w.stopCh:
			return
		case <-ctx.Done():
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.handleEvent(ctx, ev)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.l.Debug().Err(err).Msg("watcher: error from kernel")
		}
	}
}

// handleEvent classifies a raw fsnotify event into:
//   - directory Create → walk subtree and Add new dirs to the kernel watch
//     set so files created inside them also generate events.
//   - file Write/Create → push into the pending map; flushLoop picks up
//     after the debounce window expires.
//   - Remove/Rename → hard-delete the row from DB immediately. This
//     matches the "Delete Selected" UX semantics — disk truth wins;
//     retention is irrelevant when the user (or another process) has
//     already removed the file.
func (w *watcher) handleEvent(ctx context.Context, ev fsnotify.Event) {
	abs := filepath.ToSlash(ev.Name)

	w.mu.Lock()
	excludes := w.excludes
	sources := w.sources
	w.mu.Unlock()

	if matchesAnyExclude(abs, excludes) {
		return
	}

	if ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
		// Could be a file or a directory we were watching. Try both:
		// drop from kernel watch set (no error if not registered) and
		// delete any matching file row.
		w.mu.Lock()
		if w.watched[abs] {
			_ = w.fsw.Remove(filepath.FromSlash(abs))
			delete(w.watched, abs)
		}
		// Forget any pending sync — the file is gone, no point hashing it.
		delete(w.pending, abs)
		w.mu.Unlock()

		if n, err := w.mgr.DeleteByAbsPath(ctx, abs); err != nil {
			w.l.Warn().Err(err).Str("path", abs).Msg("watcher: delete row failed")
		} else if n > 0 {
			w.l.Info().Str("path", abs).Int64("rows", n).Msg("watcher: deleted row")
		}
		return
	}

	if ev.Op&fsnotify.Create != 0 {
		// New entry — if it's a directory we need to register it (and
		// its descendants) so future events under it are captured.
		// Only stat on Create to avoid an extra syscall per Write.
		if info, err := os.Stat(ev.Name); err == nil && info.IsDir() {
			w.addDirRecursive(ev.Name)
			// Files created together with the dir (rare for credential
			// flows but possible) generate their own Create events
			// once the dir is registered; nothing to schedule here.
			return
		}
	}

	if ev.Op&(fsnotify.Write|fsnotify.Create) == 0 {
		// Chmod-only or attribute change — content didn't move.
		return
	}

	// Must be covered by an enabled source to be eligible for sync.
	if _, ok := coveringInstance(abs, sources); !ok {
		return
	}

	w.mu.Lock()
	w.pending[abs] = time.Now()
	w.mu.Unlock()
}

// addDirRecursive walks under root and registers every non-excluded dir.
// Idempotent: dirs already in w.watched are skipped.
func (w *watcher) addDirRecursive(root string) {
	w.mu.Lock()
	excludes := w.excludes
	w.mu.Unlock()

	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		abs := filepath.ToSlash(p)
		if matchesAnyExclude(abs, excludes) {
			return filepath.SkipDir
		}
		w.mu.Lock()
		if !w.watched[abs] {
			if addErr := w.fsw.Add(p); addErr == nil {
				w.watched[abs] = true
			}
		}
		w.mu.Unlock()
		return nil
	})
}

// flushLoop drains pending events whose last update is older than the
// debounce window. Runs at half the debounce period so worst-case extra
// latency on top of the debounce itself is bounded.
func (w *watcher) flushLoop(ctx context.Context) {
	defer w.wg.Done()
	tickDur := w.debounce / 2
	if tickDur < 100*time.Millisecond {
		tickDur = 100 * time.Millisecond
	}
	t := time.NewTicker(tickDur)
	defer t.Stop()

	for {
		select {
		case <-w.stopCh:
			return
		case <-ctx.Done():
			return
		case <-t.C:
			w.flush(ctx)
		}
	}
}

func (w *watcher) flush(ctx context.Context) {
	now := time.Now()

	w.mu.Lock()
	if len(w.pending) == 0 {
		w.mu.Unlock()
		return
	}
	ready := make([]string, 0, len(w.pending))
	for path, ts := range w.pending {
		if now.Sub(ts) >= w.debounce {
			ready = append(ready, path)
			delete(w.pending, path)
		}
	}
	sources := w.sources
	w.mu.Unlock()

	if len(ready) == 0 {
		return
	}

	for _, abs := range ready {
		ins, ok := coveringInstance(abs, sources)
		if !ok {
			continue
		}
		changed, skipped, err := w.mgr.SyncFile(ctx, ins, abs)
		if err != nil {
			// Don't log ENOENT noisily — race between Write and a
			// subsequent Remove can leave a stale entry in pending.
			if !errors.Is(err, fs.ErrNotExist) {
				w.l.Debug().Err(err).Str("path", abs).Msg("watcher: sync file failed")
			}
			continue
		}
		// Info on real writes so the user can confirm the watcher is
		// doing work; Debug on hash-match skips so idle editors
		// (touching mtime without content change) don't spam the log.
		switch {
		case changed > 0:
			w.l.Info().
				Str("provider", string(ins.Type)).
				Str("instance", ins.Name).
				Str("path", abs).
				Msg("watcher: synced")
		case skipped > 0:
			w.l.Debug().Str("path", abs).Msg("watcher: skipped (hash match)")
		}
	}
}

// coveringInstance returns the deepest enabled non-exclude source that
// covers abs. Same matching rules as pickRetention; kept as a separate
// helper because pickRetention only returns the retention int, not the
// owning provider/instance pair the watcher needs to call SyncFile.
func coveringInstance(abs string, sources []entity.ProviderStorageSource) (provider.Instance, bool) {
	a := normAbs(abs)
	bestLen := -1
	var best entity.ProviderStorageSource
	for _, s := range sources {
		if !s.Enabled || s.Mode == "exclude" {
			continue
		}
		sp := normAbs(s.SyncPath)
		match := false
		if s.Mode == "single" {
			match = sp == a
		} else {
			match = a == sp || strings.HasPrefix(a, sp+"/")
		}
		if !match {
			continue
		}
		if len(sp) > bestLen {
			bestLen = len(sp)
			best = s
		}
	}
	if bestLen < 0 {
		return provider.Instance{}, false
	}
	return SourceToInstance(best), true
}
