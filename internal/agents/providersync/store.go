package providersync

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/yogasw/wick/internal/entity"
)

// store wraps DB ops for provider_storage + provider_storage_sources.

type store struct {
	db *gorm.DB
}

func newStore(db *gorm.DB) *store { return &store{db: db} }

// ensureFolderChain ensures all ancestor folder rows exist for relPath.
// relPath is an absolute path (slash-normalised); folder rows carry the
// cumulative absolute path so each folder maps 1:1 to its real on-disk
// location. The leading "/" on POSIX is preserved by storing each folder's
// rel_path with the same prefix as the file.
// Returns the parent_id for the direct parent of relPath.
func (s *store) ensureFolderChain(ctx context.Context, providerType, instanceName, relPath string) (uint, error) {
	norm := filepath.ToSlash(relPath)
	leadingSlash := strings.HasPrefix(norm, "/")
	trimmed := strings.TrimPrefix(norm, "/")
	parts := strings.Split(trimmed, "/")
	if len(parts) <= 1 {
		// file at the filesystem root — no folder chain to build
		return entity.RootParentID, nil
	}
	folders := parts[:len(parts)-1]
	parentID := entity.RootParentID
	for i, seg := range folders {
		if seg == "" {
			continue
		}
		cum := strings.Join(parts[:i+1], "/")
		if leadingSlash {
			cum = "/" + cum
		}
		// SELECT-then-INSERT instead of ON CONFLICT: the row has two
		// unique indexes (rel_path AND parent_id+name) that always
		// match together for a given absolute path, but SQLite errors
		// when an ON CONFLICT target only names one of them.
		var existing entity.ProviderStorage
		err := s.db.WithContext(ctx).
			Where("provider_type = ? AND instance_name = ? AND parent_id = ? AND name = ?",
				providerType, instanceName, parentID, seg).
			First(&existing).Error
		if err == nil {
			parentID = existing.ID
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, err
		}
		// Orphan recovery: a previous sync may have left a folder row
		// at this rel_path whose parent_id points to a now-deleted
		// ancestor (e.g. drive-letter row got deleted and recreated
		// with a new ID, but descendants still reference the old ID).
		// Re-parent it instead of inserting a duplicate — the rel_path
		// unique index would block the INSERT anyway.
		var orphan entity.ProviderStorage
		if rpErr := s.db.WithContext(ctx).
			Where("provider_type = ? AND instance_name = ? AND rel_path = ?",
				providerType, instanceName, cum).
			First(&orphan).Error; rpErr == nil {
			if err := s.db.WithContext(ctx).
				Model(&entity.ProviderStorage{}).
				Where("id = ?", orphan.ID).
				Update("parent_id", parentID).Error; err != nil {
				return 0, err
			}
			parentID = orphan.ID
			continue
		}
		folder := entity.ProviderStorage{
			ProviderType: providerType,
			InstanceName: instanceName,
			RelPath:      cum,
			ParentID:     parentID,
			Name:         seg,
			IsDir:        true,
			ContentHash:  "",
			SyncedAt:     time.Now().UTC(),
		}
		if err := s.db.WithContext(ctx).Create(&folder).Error; err != nil {
			return 0, err
		}
		parentID = folder.ID
	}
	return parentID, nil
}

// repairOrphans rewires every row's parent_id from its rel_path so that a
// row at "/a/b/c" is parented to the row at "/a/b". Heals DBs where a parent
// row was deleted but its descendants still reference the dead ID, which is
// what causes `listChildren(parent)` to return empty even though the rows
// are physically in the table. Cheap in-place repair; safe to run on every
// boot.
func (s *store) repairOrphans(ctx context.Context) (int, error) {
	const sep = "\x00"
	byKey := make(map[string]uint)

	var firstBatch []entity.ProviderStorage
	if err := s.db.WithContext(ctx).FindInBatches(&firstBatch, 500,
		func(tx *gorm.DB, batch int) error {
			for _, r := range firstBatch {
				byKey[r.ProviderType+sep+r.InstanceName+sep+r.RelPath] = r.ID
			}
			return nil
		}).Error; err != nil {
		return 0, err
	}

	fixed := 0
	var secondBatch []entity.ProviderStorage
	err := s.db.WithContext(ctx).FindInBatches(&secondBatch, 500,
		func(tx *gorm.DB, batch int) error {
			for _, r := range secondBatch {
				norm := filepath.ToSlash(r.RelPath)
				leadingSlash := strings.HasPrefix(norm, "/")
				trimmed := strings.TrimPrefix(norm, "/")
				parts := strings.Split(trimmed, "/")
				var wantParent uint
				if len(parts) <= 1 {
					wantParent = entity.RootParentID
				} else {
					parentRel := strings.Join(parts[:len(parts)-1], "/")
					if leadingSlash {
						parentRel = "/" + parentRel
					}
					wantParent = byKey[r.ProviderType+sep+r.InstanceName+sep+parentRel]
					// parent missing → treat as root so the row remains reachable
					// via listRoots (better than an unreachable orphan).
				}
				if r.ParentID == wantParent {
					continue
				}
				if err := s.db.WithContext(ctx).
					Model(&entity.ProviderStorage{}).
					Where("id = ?", r.ID).
					Update("parent_id", wantParent).Error; err != nil {
					return err
				}
				fixed++
			}
			return nil
		}).Error
	return fixed, err
}

// pruneEmptyFolders removes folder rows under (providerType, instanceName)
// that have no descendants. Iterates until a sweep finds nothing to delete
// so chains like /a/b/c become /a, then deleted entirely when /a has no
// children either. Safe to call repeatedly.
func (s *store) pruneEmptyFolders(ctx context.Context, providerType, instanceName string) error {
	for {
		var victims []uint
		err := s.db.WithContext(ctx).
			Model(&entity.ProviderStorage{}).
			Select("id").
			Where(`provider_type = ? AND instance_name = ? AND is_dir = ?
                   AND id NOT IN (
                     SELECT DISTINCT parent_id FROM provider_storage
                     WHERE provider_type = ? AND instance_name = ? AND parent_id != 0
                   )`, providerType, instanceName, true,
				providerType, instanceName).
			Pluck("id", &victims).Error
		if err != nil {
			return err
		}
		if len(victims) == 0 {
			return nil
		}
		if err := s.db.WithContext(ctx).
			Where("id IN ?", victims).
			Delete(&entity.ProviderStorage{}).Error; err != nil {
			return err
		}
	}
}

// wipeLegacyRelPathRows removes rows from the pre-absolute-path era. New code
// stores rel_path as an absolute filesystem path (starts with "/" on POSIX or
// a "X:" drive-letter prefix on Windows, including the bare drive root row
// "C:" itself). Anything else is legacy data that would restore to wrong
// locations. Safe to call repeatedly — only matches non-absolute rows.
func (s *store) wipeLegacyRelPathRows(ctx context.Context) error {
	res := s.db.WithContext(ctx).
		Where("rel_path NOT LIKE '/%' AND rel_path NOT LIKE '_:%'").
		Delete(&entity.ProviderStorage{})
	return res.Error
}

// fileHash returns the stored content_hash and retention_days for a file row,
// or ("", 0, false) when no row exists yet.
func (s *store) fileHash(ctx context.Context, providerType, instanceName, relPath string) (hash string, retention int, ok bool) {
	var row entity.ProviderStorage
	err := s.db.WithContext(ctx).
		Select("content_hash", "retention_days").
		Where("provider_type = ? AND instance_name = ? AND rel_path = ?", providerType, instanceName, relPath).
		First(&row).Error
	if err != nil {
		return "", 0, false
	}
	return row.ContentHash, row.RetentionDays, true
}

// upsertFile writes a file row unconditionally. Callers are responsible
// for checking whether a write is necessary before calling.
func (s *store) upsertFile(ctx context.Context, row entity.ProviderStorage) error {
	parentID, err := s.ensureFolderChain(ctx, row.ProviderType, row.InstanceName, row.RelPath)
	if err != nil {
		return err
	}
	row.ParentID = parentID
	row.Name = filepath.Base(filepath.FromSlash(row.RelPath))
	return s.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "provider_type"}, {Name: "instance_name"}, {Name: "rel_path"}},
			DoUpdates: clause.AssignmentColumns([]string{"content", "content_hash", "synced_at", "parent_id", "name", "retention_days"}),
		}).Create(&row).Error
}

// listFiles returns all rows for a provider instance.
func (s *store) listFiles(ctx context.Context, providerType, instanceName string) ([]entity.ProviderStorage, error) {
	var rows []entity.ProviderStorage
	err := s.db.WithContext(ctx).
		Where("provider_type = ? AND instance_name = ?", providerType, instanceName).
		Find(&rows).Error
	return rows, err
}

// listAll returns all rows across all providers.
func (s *store) listAll(ctx context.Context) ([]entity.ProviderStorage, error) {
	var rows []entity.ProviderStorage
	err := s.db.WithContext(ctx).Order("provider_type, instance_name, rel_path").Find(&rows).Error
	return rows, err
}

// getByID fetches one row by primary key.
func (s *store) getByID(ctx context.Context, id uint) (entity.ProviderStorage, error) {
	var row entity.ProviderStorage
	err := s.db.WithContext(ctx).First(&row, id).Error
	return row, err
}

// setRetention updates retention_days for one row.
func (s *store) setRetention(ctx context.Context, id uint, days int) error {
	return s.db.WithContext(ctx).
		Model(&entity.ProviderStorage{}).
		Where("id = ?", id).
		Update("retention_days", days).Error
}

// deleteByID removes one row. If the row is a folder, recursively removes
// every descendant so the caller doesn't strand orphan rows behind it.
func (s *store) deleteByID(ctx context.Context, id uint) error {
	var row entity.ProviderStorage
	if err := s.db.WithContext(ctx).First(&row, id).Error; err != nil {
		return err
	}
	if !row.IsDir {
		return s.db.WithContext(ctx).Delete(&entity.ProviderStorage{}, id).Error
	}
	return s.deleteSubtree(ctx, id, row.ProviderType, row.InstanceName)
}

// deleteSubtree BFS-walks descendants of id and deletes them along with id
// itself in one batch. Scoped to (providerType, instanceName) so a stray
// matching parent_id in another instance can't drag unrelated rows down.
func (s *store) deleteSubtree(ctx context.Context, id uint, providerType, instanceName string) error {
	toDelete := []uint{id}
	queue := []uint{id}
	for len(queue) > 0 {
		var children []uint
		if err := s.db.WithContext(ctx).
			Model(&entity.ProviderStorage{}).
			Where("provider_type = ? AND instance_name = ? AND parent_id IN ?",
				providerType, instanceName, queue).
			Pluck("id", &children).Error; err != nil {
			return err
		}
		queue = children
		toDelete = append(toDelete, children...)
	}
	return s.db.WithContext(ctx).
		Where("id IN ?", toDelete).
		Delete(&entity.ProviderStorage{}).Error
}

// deleteByAbsPath removes the file row(s) at the given absolute (slash-
// normalised) rel_path, regardless of provider/instance. Used by the
// realtime watcher when fsnotify reports Remove/Rename — disk truth is
// the only truth, so the row is hard-deleted instead of waiting for the
// retention job. Folder rows are left alone; pruneEmptyFolders cleans
// them on the next sync pass if they go empty.
func (s *store) deleteByAbsPath(ctx context.Context, abs string) (int64, error) {
	res := s.db.WithContext(ctx).
		Where("rel_path = ? AND is_dir = ?", abs, false).
		Delete(&entity.ProviderStorage{})
	return res.RowsAffected, res.Error
}

// deleteByInstance removes all rows for a provider instance.
func (s *store) deleteByInstance(ctx context.Context, providerType, instanceName string) (int64, error) {
	res := s.db.WithContext(ctx).
		Where("provider_type = ? AND instance_name = ?", providerType, instanceName).
		Delete(&entity.ProviderStorage{})
	return res.RowsAffected, res.Error
}

// purgeExpired deletes file rows where retention_days > 0 and synced_at is older than retention.
func (s *store) purgeExpired(ctx context.Context) (int64, error) {
	now := time.Now().UTC()
	res := s.db.WithContext(ctx).
		Where("retention_days > 0 AND synced_at < ?", now.Add(-24*time.Hour)).
		// dynamically: synced_at < now - retention_days * 24h
		// SQLite/Postgres compatible via raw:
		Where("datetime(synced_at, '+' || retention_days || ' days') < datetime(?)", now.Format(time.RFC3339)).
		Delete(&entity.ProviderStorage{})
	return res.RowsAffected, res.Error
}

// upsertFileContent is a direct content upsert (used by manual upload).
func (s *store) upsertFileContent(ctx context.Context, row entity.ProviderStorage) error {
	parentID, err := s.ensureFolderChain(ctx, row.ProviderType, row.InstanceName, row.RelPath)
	if err != nil {
		return err
	}
	row.ParentID = parentID
	row.Name = filepath.Base(filepath.FromSlash(row.RelPath))
	return s.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "provider_type"}, {Name: "instance_name"}, {Name: "rel_path"}},
			DoUpdates: clause.AssignmentColumns([]string{"content", "content_hash", "synced_at", "parent_id", "name"}),
		}).Create(&row).Error
}

// listChildren returns direct children (files + folders) under parentID.
// parentID=0 (RootParentID) means instance root.
func (s *store) listChildren(ctx context.Context, providerType, instanceName string, parentID uint) ([]entity.ProviderStorage, error) {
	var rows []entity.ProviderStorage
	err := s.db.WithContext(ctx).
		Where("provider_type = ? AND instance_name = ? AND parent_id = ?", providerType, instanceName, parentID).
		Order("is_dir DESC, name ASC").
		Find(&rows).Error
	return rows, err
}

// listRoots returns top-level rows (parent_id=0) across all instances so the
// Storage UI shows real paths instead of instance aggregates.
func (s *store) listRoots(ctx context.Context) ([]entity.ProviderStorage, error) {
	var rows []entity.ProviderStorage
	err := s.db.WithContext(ctx).
		Where("parent_id = ?", entity.RootParentID).
		Order("is_dir DESC, name ASC").
		Find(&rows).Error
	return rows, err
}

// ── Sources ───────────────────────────────────────────────────────────

func (s *store) listSources(ctx context.Context) ([]entity.ProviderStorageSource, error) {
	var rows []entity.ProviderStorageSource
	err := s.db.WithContext(ctx).Order("provider_type, instance_name, id").Find(&rows).Error
	return rows, err
}

func (s *store) getSource(ctx context.Context, id uint) (entity.ProviderStorageSource, error) {
	var row entity.ProviderStorageSource
	err := s.db.WithContext(ctx).First(&row, id).Error
	return row, err
}

func (s *store) saveSource(ctx context.Context, src entity.ProviderStorageSource) (entity.ProviderStorageSource, error) {
	src.SyncPath = filepath.Clean(src.SyncPath)
	now := time.Now().UTC()
	if src.ID == 0 {
		src.CreatedAt = now
		src.UpdatedAt = now
		err := s.db.WithContext(ctx).Create(&src).Error
		return src, err
	}
	src.UpdatedAt = now
	err := s.db.WithContext(ctx).Save(&src).Error
	return src, err
}

func (s *store) deleteSource(ctx context.Context, id uint) error {
	return s.db.WithContext(ctx).Delete(&entity.ProviderStorageSource{}, id).Error
}

func (s *store) listSourcesForInstance(ctx context.Context, providerType, instanceName string) ([]entity.ProviderStorageSource, error) {
	var rows []entity.ProviderStorageSource
	err := s.db.WithContext(ctx).
		Where("provider_type = ? AND instance_name = ? AND enabled = ?", providerType, instanceName, true).
		Find(&rows).Error
	return rows, err
}
