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
// suitable for SyncOne / backup calls.
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

// RestoreSelected writes specific DB rows (by ID) back to filesystem.
// srcsByInstance maps "providerType/instanceName" → []SrcInfo for path resolution.
// Returns count of files written.
func (m *Manager) RestoreSelected(ctx context.Context, ids []uint, srcsByInstance map[string][]SrcInfo) (int, error) {
	count := 0
	for _, id := range ids {
		row, err := m.store.getByID(ctx, id)
		if err != nil {
			return count, err
		}
		srcs := srcsByInstance[row.ProviderType+"/"+row.InstanceName]
		dst := resolveDst(srcs, row.RelPath)
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

// RestoreAll writes all DB file rows back to filesystem using configured sources.
// Handles both "single" (syncPath = full file path) and "folder" (syncPath = dir) modes.
// Call once at startup.
func (m *Manager) RestoreAll(ctx context.Context) error {
	sources, err := m.store.listSources(ctx)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return nil
	}

	rows, err := m.store.listAll(ctx)
	if err != nil {
		return err
	}

	// index: providerType/instanceName → []SrcInfo (may have multiple sources per instance)
	byInstance := make(map[string][]SrcInfo)
	for _, s := range sources {
		if !s.Enabled {
			continue
		}
		key := s.ProviderType + "/" + s.InstanceName
		byInstance[key] = append(byInstance[key], SrcInfo{s.Mode, s.SyncPath})
	}

	count := 0
	for _, row := range rows {
		if row.IsDir {
			continue
		}
		srcs := byInstance[row.ProviderType+"/"+row.InstanceName]
		if len(srcs) == 0 {
			continue
		}
		dst := resolveDst(srcs, row.RelPath)
		if dst == "" {
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
	log.Info().Int("count", count).Msg("providersync: restored files from DB")
	return nil
}

func resolveDst(srcs []SrcInfo, relPath string) string {
	rel := filepath.FromSlash(relPath)
	// prefer single-mode match first
	for _, s := range srcs {
		if s.Mode == "single" && filepath.Base(s.SyncPath) == filepath.Base(rel) {
			return s.SyncPath
		}
	}
	// fallback: folder mode
	for _, s := range srcs {
		if s.Mode != "single" {
			return filepath.Join(s.SyncPath, rel)
		}
	}
	return ""
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

// ListRoots returns distinct (provider_type, instance_name) pairs with file count.
func (m *Manager) ListRoots(ctx context.Context) ([]RootInfo, error) {
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

// SaveSource creates or updates a sync source and immediately runs a backup pass.
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
	return saved, nil
}

// DeleteSource removes a sync source by ID.
func (m *Manager) DeleteSource(ctx context.Context, id uint) error {
	return m.store.deleteSource(ctx, id)
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
	files, err := collectFiles(ins.Storage)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for rel, content := range files {
		row := entity.ProviderStorage{
			ProviderType: string(ins.Type),
			InstanceName: ins.Name,
			RelPath:      rel,
			Content:      content,
			ContentHash:  hashBytes(content),
			SyncedAt:     now,
		}
		written, err := m.store.upsertFile(ctx, row)
		if err != nil {
			return err
		}
		if written {
			log.Debug().
				Str("provider", string(ins.Type)).
				Str("instance", ins.Name).
				Str("file", rel).
				Msg("providersync: stored")
		}
	}
	return nil
}

func collectFiles(sc *provider.StorageConfig) (map[string][]byte, error) {
	out := make(map[string][]byte)
	if sc.Mode == "single" {
		data, err := os.ReadFile(sc.SyncPath)
		if err != nil {
			return nil, err
		}
		out[filepath.Base(sc.SyncPath)] = data
		return out, nil
	}
	err := filepath.WalkDir(sc.SyncPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			log.Debug().Err(err).Str("path", path).Msg("providersync: skipping unreadable entry")
			return nil
		}
		if d.IsDir() {
			return nil
		}
		// skip non-regular files (symlinks, devices, pipes, Windows reparse points)
		info, err := d.Info()
		if err != nil || info.Mode()&os.ModeType != 0 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			log.Debug().Err(err).Str("path", path).Msg("providersync: skipping unreadable file")
			return nil
		}
		rel, _ := filepath.Rel(sc.SyncPath, path)
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	return out, err
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
