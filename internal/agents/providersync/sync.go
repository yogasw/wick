// Package providersync syncs provider credential files to/from the DB.
//
// On startup: DB → filesystem (restore).
// Background ticker: filesystem → DB (backup), skipping unchanged files
// via SHA-256 hash comparison.
// Retention job: purges expired file rows on each tick.
package providersync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
}

// New returns a Manager backed by db.
func New(db *gorm.DB) *Manager {
	return &Manager{
		db:    db,
		store: newStore(db),
	}
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
func (m *Manager) SyncOne(ctx context.Context, ins provider.Instance) error {
	if ins.Storage == nil {
		return nil
	}
	return m.backup(ctx, ins)
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
		if err := m.SyncOne(ctx, SourceToInstance(src)); err != nil {
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

// RestoreAll writes all DB file rows back to filesystem.
// rel_path is now an absolute path; we only restore files whose absolute path
// falls under at least one enabled source (file mode "single") or under an
// enabled folder source's prefix. Call once at startup.
func (m *Manager) RestoreAll(ctx context.Context) error {
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

	rows, err := m.store.listAll(ctx)
	if err != nil {
		return err
	}

	count := 0
	skippedExist := 0
	skippedDiverged := 0
	for _, row := range rows {
		if row.IsDir {
			continue
		}
		if !sourceCovers(row.RelPath, enabled) {
			continue
		}
		dst := absToOS(row.RelPath)
		if dst == "" {
			continue
		}
		// Disk is source of truth at startup: only fill gaps.
		// - missing on disk → write from DB
		// - exists, same hash → no-op
		// - exists, diverged → keep disk, log
		if existing, err := os.ReadFile(dst); err == nil {
			if hashBytes(existing) == row.ContentHash {
				skippedExist++
				continue
			}
			skippedDiverged++
			log.Warn().Str("dst", dst).Msg("providersync: disk diverged from DB — keeping disk copy")
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			log.Warn().Err(err).Str("dst", dst).Msg("providersync: restore mkdir failed")
			continue
		}
		if err := os.WriteFile(dst, row.Content, 0o600); err != nil {
			log.Warn().Err(err).Str("dst", dst).Msg("providersync: restore write failed")
			continue
		}
		count++
	}
	log.Info().
		Int("restored", count).
		Int("skipped_match", skippedExist).
		Int("skipped_diverged", skippedDiverged).
		Msg("providersync: RestoreAll done")
	return nil
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
func (m *Manager) Upload(ctx context.Context, providerType, instanceName, relPath string, content []byte) error {
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
		if syncErr := m.SyncOne(ctx, ins); syncErr != nil {
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

func (m *Manager) backup(ctx context.Context, ins provider.Instance) error {
	// Skip exclude-mode pseudo-sources here — only include-mode sources
	// drive disk walks. Retention picking ignores them too.
	if ins.Storage != nil && (ins.Storage.Mode == "exclude") {
		return nil
	}

	allSources, _ := m.store.listSourcesForInstance(ctx, string(ins.Type), ins.Name)
	// stable order so retention pick is deterministic when two sources have
	// the same SyncPath length (shouldn't happen, but cheap insurance).
	sort.SliceStable(allSources, func(i, j int) bool { return allSources[i].ID < allSources[j].ID })

	excludes := collectExcludePatterns(allSources)

	files, err := collectFiles(ins.Storage, excludes)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	for abs, content := range files {
		row := entity.ProviderStorage{
			ProviderType:  string(ins.Type),
			InstanceName:  ins.Name,
			RelPath:       abs,
			Content:       content,
			ContentHash:   hashBytes(content),
			SyncedAt:      now,
			RetentionDays: pickRetention(abs, allSources),
		}
		written, err := m.store.upsertFile(ctx, row)
		if err != nil {
			return err
		}
		if written {
			log.Debug().
				Str("provider", string(ins.Type)).
				Str("instance", ins.Name).
				Str("file", abs).
				Msg("providersync: stored")
		}
	}
	return nil
}

// collectFiles returns a map keyed by absolute (slash-normalised) path so the
// row identity matches the real filesystem location, regardless of which
// configured source produced it. Overlapping sources therefore dedupe instead
// of stacking. Paths matching any of the excludes glob list are skipped.
func collectFiles(sc *provider.StorageConfig, excludes []string) (map[string][]byte, error) {
	out := make(map[string][]byte)
	base := filepath.Clean(sc.SyncPath)
	if sc.Mode == "single" {
		abs := filepath.ToSlash(base)
		if matchesAnyExclude(abs, excludes) {
			return out, nil
		}
		data, err := os.ReadFile(base)
		if err != nil {
			return nil, err
		}
		out[abs] = data
		return out, nil
	}
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Debug().Err(err).Str("path", path).Msg("providersync: skipping unreadable entry")
			return nil
		}
		abs := filepath.ToSlash(path)
		if d.IsDir() {
			// Prune the whole subtree if the directory itself matches.
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
		data, err := os.ReadFile(path)
		if err != nil {
			log.Debug().Err(err).Str("path", path).Msg("providersync: skipping unreadable file")
			return nil
		}
		out[abs] = data
		return nil
	})
	return out, err
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
