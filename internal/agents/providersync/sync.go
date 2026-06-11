// Package providersync syncs provider credential files to/from the DB.
//
// On startup: DB → filesystem (restore).
// Background ticker: filesystem → DB (backup), skipping unchanged files
// via SHA-256 hash comparison.
// Retention job: purges expired file rows on each tick.
package providersync

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/entity"
)

// Manager drives syncing for all provider instances that have Storage
// configured. One Manager per app lifetime; call Start once.
type Manager struct {
	db    *gorm.DB
	store *store

	// watcherMu guards the realtime fsnotify watcher lifecycle. The
	// watcher itself is lazily created by EnsureWatcher and torn down
	// by StopWatcher; nil means "not running" (cron-only mode).
	watcherMu sync.Mutex
	w         *watcher
}

// New returns a Manager backed by db.
func New(db *gorm.DB) *Manager {
	return &Manager{
		db:    db,
		store: newStore(db),
	}
}

// EnsureWatcher starts the realtime fsnotify watcher if it isn't already
// running, or hot-reloads its source set if it is. Idempotent — safe to
// call every job tick. debounceMs <= 0 falls back to 1000.
//
// Boot order matters: the caller (server.go) MUST run RestoreAllForce
// to completion before calling EnsureWatcher, otherwise the watcher
// races the restore writes and reports them as "disk changes" back to
// itself.
func (m *Manager) EnsureWatcher(ctx context.Context, debounceMs int) error {
	m.watcherMu.Lock()
	defer m.watcherMu.Unlock()

	if debounceMs <= 0 {
		debounceMs = 1000
	}
	debounce := time.Duration(debounceMs) * time.Millisecond

	sources, err := m.store.listSources(ctx)
	if err != nil {
		return err
	}

	if m.w != nil {
		// Update debounce in place (next flush tick picks it up via
		// recompute) and reconcile sources.
		m.w.mu.Lock()
		m.w.debounce = debounce
		m.w.mu.Unlock()
		return m.w.sync(ctx, sources)
	}

	w, err := newWatcher(m, debounce)
	if err != nil {
		return err
	}
	// Use a detached context for the watcher's lifetime — the job
	// context that triggered EnsureWatcher cancels after a single tick,
	// but the watcher must outlive it. The watcher exits via stop().
	if err := w.start(context.Background(), sources); err != nil {
		_ = w.fsw.Close()
		return err
	}
	m.w = w
	return nil
}

// StopWatcher tears down the realtime watcher. No-op when not running.
// Pending debounced events are dropped — the next cron tick will pick
// them up via the normal walk path.
func (m *Manager) StopWatcher() {
	m.watcherMu.Lock()
	defer m.watcherMu.Unlock()
	if m.w == nil {
		return
	}
	m.w.stop()
	m.w = nil
}

// Reload tells the realtime watcher to re-read sources from DB and
// adjust its kernel watch set accordingly. No-op when the watcher is
// not running. Called after SaveSource / DeleteSource so toggling a
// row in the Settings UI propagates without a job tick.
func (m *Manager) Reload(ctx context.Context) error {
	m.watcherMu.Lock()
	w := m.w
	m.watcherMu.Unlock()
	if w == nil {
		return nil
	}
	sources, err := m.store.listSources(ctx)
	if err != nil {
		return err
	}
	return w.sync(ctx, sources)
}

// SourceToInstance converts a ProviderStorageSource to a provider.Instance
// suitable for SyncOne / backup calls. Excludes are read separately at the
// store layer since provider.Instance has no slot for them.
func SourceToInstance(src entity.ProviderStorageSource) provider.Instance {
	return provider.Instance{
		Type: provider.Type(src.ProviderType),
		Name: src.InstanceName,
		Storage: &provider.StorageConfig{
			Mode:     src.Mode,
			SyncPath: src.SyncPath,
		},
	}
}

// SyncOne runs a single backup pass for one instance.
// SyncOne backs up one provider instance from disk to DB.
// Returns (changed, skipped, error) — changed is the number of files
// written/updated; skipped is files whose content hash was unchanged.
func (m *Manager) SyncOne(ctx context.Context, ins provider.Instance, verbose ...bool) (int, int, error) {
	if ins.Storage == nil {
		return 0, 0, nil
	}
	v := len(verbose) > 0 && verbose[0]
	return m.backup(ctx, ins, v)
}

// PurgeExcluded walks every row for (providerType, instanceName) and deletes
// rows whose absolute rel_path matches any current enabled-source exclude
// pattern. Empty folder rows left behind are also pruned so the tree
// doesn't show ghost directories. Returns the number of file rows deleted.
func (m *Manager) PurgeExcluded(ctx context.Context, providerType, instanceName string) (int, error) {
	sources, err := m.store.listSourcesForInstance(ctx, providerType, instanceName)
	if err != nil {
		return 0, err
	}
	patterns := collectExcludePatterns(sources)
	if len(patterns) == 0 {
		return 0, nil
	}
	rows, err := m.store.listFiles(ctx, providerType, instanceName)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, r := range rows {
		if r.IsDir {
			continue
		}
		if !matchesAnyExclude(r.RelPath, patterns) {
			continue
		}
		if err := m.store.deleteByID(ctx, r.ID); err != nil {
			return deleted, err
		}
		deleted++
	}
	// Drop folders that became empty after the file purge — keeps the
	// explorer view tidy.
	if err := m.store.pruneEmptyFolders(ctx, providerType, instanceName); err != nil {
		log.Warn().Err(err).Msg("providersync: prune empty folders failed")
	}
	return deleted, nil
}

// RepairTree rewires parent_id values for every row from its rel_path so
// listChildren / explorer drill-down works even when a parent row was
// previously deleted (orphan recovery). Returns the number of rows fixed.
// Cheap; intended for boot-time and after destructive operations.
func (m *Manager) RepairTree(ctx context.Context) (int, error) {
	return m.store.repairOrphans(ctx)
}

// SyncAll iterates all enabled sources and runs a backup pass for each.
// Returns number of sources that synced cleanly; per-source errors are
// logged but do not abort the loop (one bad path shouldn't stop the others).
// Safe to call on startup so users see populated rows immediately without
// having to wait for the cron tick.
func (m *Manager) SyncAll(ctx context.Context) (int, error) {
	sources, err := m.store.listSources(ctx)
	if err != nil {
		return 0, err
	}
	synced := 0
	for _, src := range sources {
		if !src.Enabled || src.Mode == "exclude" {
			continue
		}
		if _, _, err := m.SyncOne(ctx, SourceToInstance(src)); err != nil {
			log.Warn().Err(err).
				Str("provider", src.ProviderType).
				Str("path", src.SyncPath).
				Msg("providersync: SyncAll source failed")
			continue
		}
		synced++
	}
	// Self-heal: purge any rows that fell through to DB before the
	// exclude rule was added. Runs once per (provider, instance) so a DB
	// with many sources doesn't redo the same sweep.
	seen := map[string]bool{}
	for _, src := range sources {
		key := src.ProviderType + "\x00" + src.InstanceName
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, err := m.PurgeExcluded(ctx, src.ProviderType, src.InstanceName); err != nil {
			log.Warn().Err(err).
				Str("provider", src.ProviderType).
				Str("instance", src.InstanceName).
				Msg("providersync: SyncAll purge failed")
		}
	}
	return synced, nil
}

// RestoreSelected writes specific DB rows (by ID) back to filesystem.
// srcsByInstance is kept for API compatibility but ignored: rel_path is now
// an absolute filesystem path, written directly. Returns count of files written.
func (m *Manager) RestoreSelected(ctx context.Context, ids []uint, srcsByInstance map[string][]SrcInfo) (int, error) {
	count := 0
	for _, id := range ids {
		row, err := m.store.getByID(ctx, id)
		if err != nil {
			return count, err
		}
		if row.IsDir {
			continue
		}
		dst := absToOS(row.RelPath)
		if dst == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return count, err
		}
		if err := os.WriteFile(dst, row.Content, 0o600); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// SrcInfo holds mode and path for one configured sync source.
type SrcInfo struct{ Mode, SyncPath string }

// RestoreAll writes all DB file rows back to filesystem with the disk-wins
// guard: missing → write, same hash → skip, diverged → keep disk + log.
// Used by the cron tick and the /restore-now UI button.
func (m *Manager) RestoreAll(ctx context.Context, verbose ...bool) error {
	return m.restoreAll(ctx, false, len(verbose) > 0 && verbose[0])
}

// RestoreAllForce overwrites every covered file on disk with the DB copy,
// no hash check. Used at server boot so DB is the source of truth on the
// first restore after a container restart (no-volume env).
func (m *Manager) RestoreAllForce(ctx context.Context, verbose ...bool) error {
	return m.restoreAll(ctx, true, len(verbose) > 0 && verbose[0])
}

func (m *Manager) restoreAll(ctx context.Context, force bool, verbose bool) error {

	l := log.With().Str("component", "provider-storage").Str("op", "restore").Str("run_id", runID()).Logger()
	// Legacy-row wipe is now a one-shot DB migration in postgres.Migrate;
	// nothing to clean up here.
	sources, err := m.store.listSources(ctx)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return nil
	}

	enabled := make([]entity.ProviderStorageSource, 0, len(sources))
	for _, s := range sources {
		if s.Enabled {
			enabled = append(enabled, s)
		}
	}
	if len(enabled) == 0 {
		return nil
	}

	total, _ := m.store.countAll(ctx)
	l.Info().Int64("total_files", total).Msg("providersync: restore started — do not interrupt")

	count := 0
	skippedExist := 0
	skippedDiverged := 0
	skippedUncovered := 0
	processed := 0
	lastPct := int64(-1)
	if err := m.store.iterAll(ctx, 50, func(row entity.ProviderStorage) error {
		processed++
		if total > 0 {
			pct := int64(processed) * 100 / total
			if pct != lastPct {
				lastPct = pct
				l.Info().
					Int64("pct", pct).
					Int("processed", processed).
					Int64("total", total).
					Msg("providersync: restoring")
			}
		}
		if row.IsDir {
			return nil
		}
		if !sourceCovers(row.RelPath, enabled) {
			skippedUncovered++
			return nil
		}
		dst := absToOS(row.RelPath)
		if dst == "" {
			return nil
		}
		// force = DB wins for diverged files, but identical files still
		// skip to keep wide restores fast and avoid noisy rewrites.
		// guard path: missing → write, same hash → skip, diverged → keep disk.
		if existing, err := os.ReadFile(dst); err == nil {
			if hashBytes(existing) == row.ContentHash {
				skippedExist++
				if verbose {
					l.Info().
						Str("file", dst).
						Str("hash", row.ContentHash[:8]).
						Int("size_bytes", len(row.Content)).
						Msg("providersync: restore skip (hash match)")
				}
				return nil
			}
			if !force {
				skippedDiverged++
				l.Warn().Str("file", dst).Msg("providersync: disk diverged from DB — keeping disk copy")
				return nil
			}
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			l.Warn().Err(err).Str("file", dst).Msg("providersync: restore mkdir failed")
			return nil
		}
		if err := os.WriteFile(dst, row.Content, 0o600); err != nil {
			l.Warn().Err(err).Str("file", dst).Msg("providersync: restore write failed")
			return nil
		}
		if verbose {
			l.Info().
				Str("file", dst).
				Str("hash", row.ContentHash[:8]).
				Int("size_bytes", len(row.Content)).
				Msg("providersync: restore written")
		}
		count++
		return nil
	}); err != nil {
		return err
	}
	l.Info().
		Int("restored", count).
		Int("skipped_match", skippedExist).
		Int("skipped_diverged", skippedDiverged).
		Int("skipped_uncovered", skippedUncovered).
		Int("processed", processed).
		Int64("total_files", total).
		Msg("providersync: RestoreAll done")
	return nil
}

// runID returns a short random hex string for correlating log lines within
// a single sync or restore run.
func runID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// absToOS converts an absolute path stored in DB (slash-normalised) to the
// platform-native form for os.WriteFile.
func absToOS(abs string) string {
	if abs == "" {
		return ""
	}
	return filepath.FromSlash(abs)
}

// normAbs returns the canonical slash form of a path used for keys/comparison.
func normAbs(p string) string {
	return filepath.ToSlash(filepath.Clean(p))
}

// sourceCovers returns true when abs is covered by any of the given sources
// (single mode = exact match; folder mode = prefix match including the dir
// itself).
func sourceCovers(abs string, sources []entity.ProviderStorageSource) bool {
	a := normAbs(abs)
	for _, s := range sources {
		if s.Mode == "exclude" {
			continue
		}
		sp := normAbs(s.SyncPath)
		if s.Mode == "single" {
			if sp == a {
				return true
			}
			continue
		}
		if a == sp || strings.HasPrefix(a, sp+"/") {
			return true
		}
	}
	return false
}

// pickRetention returns the RetentionDays of the deepest enabled source whose
// path covers abs. Falls back to 0 (lifetime) when no source matches.
func pickRetention(abs string, sources []entity.ProviderStorageSource) int {
	a := normAbs(abs)
	bestLen := -1
	days := 0
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
			days = s.RetentionDays
		}
	}
	return days
}

// Upload stores file content directly into DB (manual upload).
//
// If relPath is already covered by an enabled include source whose
// provider/instance differs from the caller's values, the upload is
// re-tagged to that source. This stops a single physical path from
// living under two (provider, instance) pairs — which would leave one
// copy uncovered by RestoreAll and effectively unrestorable.
func (m *Manager) Upload(ctx context.Context, providerType, instanceName, relPath string, content []byte) error {
	if sources, err := m.store.listSources(ctx); err == nil {
		// Pick the deepest covering enabled include source so nested
		// sources (e.g. /a/b inside /a) win over the shallower one.
		bestLen := -1
		a := normAbs(relPath)
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
				providerType = s.ProviderType
				instanceName = s.InstanceName
			}
		}
	}
	hash := hashBytes(content)
	row := entity.ProviderStorage{
		ProviderType: providerType,
		InstanceName: instanceName,
		RelPath:      relPath,
		Content:      content,
		ContentHash:  hash,
		SyncedAt:     time.Now().UTC(),
	}
	return m.store.upsertFileContent(ctx, row)
}

// SetRetention updates retention_days for one file row.
func (m *Manager) SetRetention(ctx context.Context, id uint, days int) error {
	return m.store.setRetention(ctx, id, days)
}

// ListAll returns all stored file rows.
func (m *Manager) ListAll(ctx context.Context) ([]entity.ProviderStorage, error) {
	return m.store.listAll(ctx)
}

// ListChildren returns direct children (files + folders) under parentID for an instance.
// parentID=0 means instance root.
func (m *Manager) ListChildren(ctx context.Context, providerType, instanceName string, parentID uint) ([]entity.ProviderStorage, error) {
	return m.store.listChildren(ctx, providerType, instanceName, parentID)
}

// ListRoots returns top-level rows (parent_id=0) across all instances.
func (m *Manager) ListRoots(ctx context.Context) ([]entity.ProviderStorage, error) {
	return m.store.listRoots(ctx)
}

// GetByID returns one file row.
func (m *Manager) GetByID(ctx context.Context, id uint) (entity.ProviderStorage, error) {
	return m.store.getByID(ctx, id)
}

// DeleteByID removes one file row from DB.
func (m *Manager) DeleteByID(ctx context.Context, id uint) error {
	return m.store.deleteByID(ctx, id)
}

// DeleteByAbsPath hard-deletes the file row at the given absolute path.
// Used by the realtime watcher in response to fsnotify Remove/Rename
// events — when a file disappears from disk it must disappear from DB
// immediately, bypassing the retention job. Returns rows affected.
func (m *Manager) DeleteByAbsPath(ctx context.Context, abs string) (int64, error) {
	return m.store.deleteByAbsPath(ctx, abs)
}

// DeleteByInstance removes all rows for a provider instance.
func (m *Manager) DeleteByInstance(ctx context.Context, providerType, instanceName string) (int64, error) {
	return m.store.deleteByInstance(ctx, providerType, instanceName)
}

// ListSources returns all configured sync sources.
func (m *Manager) ListSources(ctx context.Context) ([]entity.ProviderStorageSource, error) {
	return m.store.listSources(ctx)
}

// GetSource returns one configured source by ID.
func (m *Manager) GetSource(ctx context.Context, id uint) (entity.ProviderStorageSource, error) {
	return m.store.getSource(ctx, id)
}

// SaveSource creates or updates a sync source and immediately runs a backup pass.
// Also recomputes per-file retention for the instance so existing rows reflect
// the new (or changed) source retention without waiting for the next sync tick.
func (m *Manager) SaveSource(ctx context.Context, src entity.ProviderStorageSource) (entity.ProviderStorageSource, error) {
	saved, err := m.store.saveSource(ctx, src)
	if err != nil {
		return saved, err
	}
	if saved.Enabled {
		ins := provider.Instance{
			Type:    provider.Type(saved.ProviderType),
			Name:    saved.InstanceName,
			Storage: &provider.StorageConfig{Mode: saved.Mode, SyncPath: saved.SyncPath},
		}
		if _, _, syncErr := m.SyncOne(ctx, ins); syncErr != nil {
			log.Warn().Err(syncErr).
				Str("provider", saved.ProviderType).
				Str("path", saved.SyncPath).
				Msg("providersync: initial sync failed")
		}
	}
	if _, err := m.RecomputeRetention(ctx, saved.ProviderType, saved.InstanceName); err != nil {
		log.Warn().Err(err).
			Str("provider", saved.ProviderType).
			Str("instance", saved.InstanceName).
			Msg("providersync: recompute retention failed")
	}
	// Apply exclude patterns retroactively — files captured before the
	// user added the exclude must drop out of DB or RestoreAll would
	// re-create them on next boot.
	if n, err := m.PurgeExcluded(ctx, saved.ProviderType, saved.InstanceName); err != nil {
		log.Warn().Err(err).
			Str("provider", saved.ProviderType).
			Str("instance", saved.InstanceName).
			Msg("providersync: purge excluded failed")
	} else if n > 0 {
		log.Info().Int("rows", n).
			Str("provider", saved.ProviderType).
			Str("instance", saved.InstanceName).
			Msg("providersync: purged excluded rows after SaveSource")
	}
	// Hot-reload the realtime watcher (if running) so it picks up the
	// new/edited source without waiting for the next job tick.
	if err := m.Reload(ctx); err != nil {
		log.Warn().Err(err).Msg("providersync: watcher reload after SaveSource failed")
	}
	return saved, nil
}

// DeleteSource removes a sync source by ID and recomputes file retention so
// rows that previously inherited this source's retention drop back to the next
// matching source (or 0 / lifetime if none).
func (m *Manager) DeleteSource(ctx context.Context, id uint) error {
	src, err := m.store.getSource(ctx, id)
	if err != nil {
		return err
	}
	if err := m.store.deleteSource(ctx, id); err != nil {
		return err
	}
	if _, err := m.RecomputeRetention(ctx, src.ProviderType, src.InstanceName); err != nil {
		log.Warn().Err(err).
			Str("provider", src.ProviderType).
			Str("instance", src.InstanceName).
			Msg("providersync: recompute retention failed after delete")
	}
	// Hot-reload the realtime watcher (if running) so removed paths
	// drop out of the kernel watch set without waiting for the next job tick.
	if err := m.Reload(ctx); err != nil {
		log.Warn().Err(err).Msg("providersync: watcher reload after DeleteSource failed")
	}
	return nil
}

// RecomputeRetention walks every file row for (providerType, instanceName)
// and rewrites retention_days based on the deepest currently-enabled source
// whose path covers the row. Returns the number of rows updated. Cheap to
// call on every source change since pickRetention is in-memory.
func (m *Manager) RecomputeRetention(ctx context.Context, providerType, instanceName string) (int, error) {
	srcs, err := m.store.listSourcesForInstance(ctx, providerType, instanceName)
	if err != nil {
		return 0, err
	}
	rows, err := m.store.listFiles(ctx, providerType, instanceName)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, r := range rows {
		if r.IsDir {
			continue
		}
		want := pickRetention(r.RelPath, srcs)
		if want == r.RetentionDays {
			continue
		}
		if err := m.store.setRetention(ctx, r.ID, want); err != nil {
			return changed, err
		}
		changed++
	}
	return changed, nil
}

// CheckSource lists file paths under syncPath without reading content.
func (m *Manager) CheckSource(mode, syncPath string) ([]string, error) {
	if mode == "single" {
		if _, err := os.Stat(syncPath); err != nil {
			return nil, err
		}
		return []string{filepath.Base(syncPath)}, nil
	}
	var out []string
	err := filepath.WalkDir(syncPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(syncPath, path)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}

// RunRetention purges expired file rows.
func (m *Manager) RunRetention(ctx context.Context) {
	if n, err := m.store.purgeExpired(ctx); err != nil {
		log.Warn().Err(err).Msg("providersync: purge expired files failed")
	} else if n > 0 {
		log.Info().Int64("count", n).Msg("providersync: purged expired file rows")
	}
}

// ── internals ─────────────────────────────────────────────────────────

func (m *Manager) backup(ctx context.Context, ins provider.Instance, verbose bool) (int, int, error) {
	// Skip exclude-mode pseudo-sources here — only include-mode sources
	// drive disk walks. Retention picking ignores them too.
	if ins.Storage != nil && (ins.Storage.Mode == "exclude") {
		return 0, 0, nil
	}

	l := log.With().Str("component", "provider-storage").Str("op", "sync").Str("run_id", runID()).Logger()

	allSources, _ := m.store.listSourcesForInstance(ctx, string(ins.Type), ins.Name)
	sort.SliceStable(allSources, func(i, j int) bool { return allSources[i].ID < allSources[j].ID })

	excludes := collectExcludePatterns(allSources)
	base := filepath.Clean(ins.Storage.SyncPath)
	changed, skipped := 0, 0

	if ins.Storage.Mode == "single" {
		abs := filepath.ToSlash(base)
		if matchesAnyExclude(abs, excludes) {
			return 0, 0, nil
		}
		// Surface stat errors so SyncAll can skip-count an unreadable
		// single-mode source (matches the pre-streaming behaviour of
		// collectFiles returning err on os.ReadFile failure).
		info, err := os.Stat(base)
		if err != nil {
			return 0, 0, err
		}
		if info.IsDir() || info.Mode()&os.ModeType != 0 {
			return 0, 0, nil
		}
		return m.syncFilePath(ctx, ins, abs, base, allSources, verbose, l)
	}

	walkErr := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Debug().Err(err).Str("path", path).Msg("providersync: skipping unreadable entry")
			return nil
		}
		abs := filepath.ToSlash(path)
		if d.IsDir() {
			if matchesAnyExclude(abs, excludes) {
				return filepath.SkipDir
			}
			return nil
		}
		if matchesAnyExclude(abs, excludes) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Mode()&os.ModeType != 0 {
			return nil
		}
		c, s, _ := m.syncFilePath(ctx, ins, abs, path, allSources, verbose, l)
		changed += c
		skipped += s
		return nil
	})

	l.Debug().
		Str("provider", string(ins.Type)).
		Str("instance", ins.Name).
		Int("changed", changed).
		Int("skipped", skipped).
		Msg("providersync: sync done")
	return changed, skipped, walkErr
}

// syncFilePath syncs a single file row by:
//  1. Streaming-hashing the file on disk (constant memory, no full read).
//  2. Comparing to the stored hash + retention — short-circuit when unchanged.
//  3. Loading content into memory ONLY when a write is necessary.
//
// This is the per-file streaming path shared by backup() walks and the
// realtime watcher. Previously backup() materialised every file into a
// single map[string][]byte before iterating; on a tree with thousands of
// files that produced hundreds of MB of RAM spike per cron tick and OOM-
// killed small containers. Streaming keeps idle scans nearly free since
// >99% of files are unchanged and never load their content.
func (m *Manager) syncFilePath(ctx context.Context, ins provider.Instance, abs, osPath string, allSources []entity.ProviderStorageSource, verbose bool, l zerolog.Logger) (int, int, error) {
	newHash, _, err := hashFileStream(osPath)
	if err != nil {
		log.Debug().Err(err).Str("path", osPath).Msg("providersync: hash failed")
		return 0, 0, nil
	}
	retention := pickRetention(abs, allSources)
	prevHash, prevRetention, exists := m.store.fileHash(ctx, string(ins.Type), ins.Name, abs)
	if exists && prevHash == newHash && prevRetention == retention {
		return 0, 1, nil
	}
	content, err := os.ReadFile(osPath)
	if err != nil {
		log.Debug().Err(err).Str("path", osPath).Msg("providersync: read failed")
		return 0, 0, nil
	}
	row := entity.ProviderStorage{
		ProviderType:  string(ins.Type),
		InstanceName:  ins.Name,
		RelPath:       abs,
		Content:       content,
		ContentHash:   newHash,
		SyncedAt:      time.Now().UTC(),
		RetentionDays: retention,
	}
	if err := m.store.upsertFile(ctx, row); err != nil {
		return 0, 0, err
	}
	if verbose {
		e := l.Info().
			Str("file", abs).
			Int("size_bytes", len(content)).
			Str("hash", newHash[:8])
		if prevHash != "" {
			e = e.Str("prev_hash", prevHash[:8])
		}
		e.Msg("providersync: sync stored")
	}
	return 1, 0, nil
}

// SyncFile syncs one absolute path on disk into DB. Used by the realtime
// watcher to react to a single Write/Create event without re-walking the
// whole source. Returns (changed, skipped, err) where skipped=1 means the
// content hash matched and no DB write happened.
func (m *Manager) SyncFile(ctx context.Context, ins provider.Instance, abs string) (int, int, error) {
	if ins.Storage == nil {
		return 0, 0, nil
	}
	allSources, _ := m.store.listSourcesForInstance(ctx, string(ins.Type), ins.Name)
	excludes := collectExcludePatterns(allSources)
	if matchesAnyExclude(abs, excludes) {
		return 0, 0, nil
	}
	osPath := filepath.FromSlash(abs)
	info, err := os.Stat(osPath)
	if err != nil {
		return 0, 0, err
	}
	if info.IsDir() || info.Mode()&os.ModeType != 0 {
		return 0, 0, nil
	}
	l := log.With().Str("component", "provider-storage").Str("op", "watch-sync").Logger()
	return m.syncFilePath(ctx, ins, abs, osPath, allSources, false, l)
}

// hashFileStream computes SHA-256 of a file without loading its full
// content into memory. Used by the streaming sync path so unchanged files
// (the common case) never allocate beyond the 32 KB io.Copy buffer.
func hashFileStream(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// hashBytes computes SHA-256 of an in-memory buffer. Retained for the
// restore path (RestoreAll diff check) and the manual Upload path, where
// the caller already holds the content in memory.
func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
