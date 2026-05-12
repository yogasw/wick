package providersync

import (
	"context"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/yogasw/wick/internal/entity"
)

// RootInfo is one (provider_type, instance_name) pair with a file count.
type RootInfo struct {
	ProviderType string
	InstanceName string
	FileCount    int64
}

// store wraps DB ops for provider_storage + provider_storage_sources.

type store struct {
	db *gorm.DB
}

func newStore(db *gorm.DB) *store { return &store{db: db} }

// ensureFolderChain ensures all ancestor folder rows exist for relPath.
// Returns the parent_id for the direct parent of relPath.
// Uses ON CONFLICT DO NOTHING so sync cycles are cheap.
func (s *store) ensureFolderChain(ctx context.Context, providerType, instanceName, relPath string) (uint, error) {
	parts := strings.Split(filepath.ToSlash(relPath), "/")
	// folders = all segments except the last (filename)
	folders := parts[:len(parts)-1]
	parentID := entity.RootParentID
	for i, seg := range folders {
		folderRelPath := strings.Join(parts[:i+1], "/") // cumulative path as unique key
		folder := entity.ProviderStorage{
			ProviderType: providerType,
			InstanceName: instanceName,
			RelPath:      folderRelPath,
			ParentID:     parentID,
			Name:         seg,
			IsDir:        true,
			ContentHash:  "",
			SyncedAt:     time.Now().UTC(),
		}
		// build rel_path for the folder as its unique path key
		// we reuse the Name field for rel_path lookup: use the same unique index
		// conflict key is (provider_type, instance_name, parent_id, name)
		if err := s.db.WithContext(ctx).
			Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "provider_type"}, {Name: "instance_name"}, {Name: "parent_id"}, {Name: "name"}},
				DoNothing: true,
			}).Create(&folder).Error; err != nil {
			return 0, err
		}
		// re-fetch to get the ID (whether just inserted or pre-existing)
		var existing entity.ProviderStorage
		if err := s.db.WithContext(ctx).
			Where("provider_type = ? AND instance_name = ? AND parent_id = ? AND name = ?",
				providerType, instanceName, parentID, seg).
			First(&existing).Error; err != nil {
			return 0, err
		}
		parentID = existing.ID
	}
	return parentID, nil
}

// upsertFile writes a file row only when hash changed. Returns true if written.
func (s *store) upsertFile(ctx context.Context, row entity.ProviderStorage) (bool, error) {
	parentID, err := s.ensureFolderChain(ctx, row.ProviderType, row.InstanceName, row.RelPath)
	if err != nil {
		return false, err
	}
	row.ParentID = parentID
	row.Name = filepath.Base(filepath.FromSlash(row.RelPath))

	var existing entity.ProviderStorage
	err = s.db.WithContext(ctx).
		Where("provider_type = ? AND instance_name = ? AND rel_path = ?",
			row.ProviderType, row.InstanceName, row.RelPath).
		First(&existing).Error
	if err == nil {
		if existing.ContentHash == row.ContentHash {
			return false, nil
		}
		// preserve user-set retention
		row.RetentionDays = existing.RetentionDays
	}
	if err := s.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "provider_type"}, {Name: "instance_name"}, {Name: "rel_path"}},
			DoUpdates: clause.AssignmentColumns([]string{"content", "content_hash", "synced_at", "parent_id", "name"}),
		}).Create(&row).Error; err != nil {
		return false, err
	}
	return true, nil
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

// deleteByID removes one file row.
func (s *store) deleteByID(ctx context.Context, id uint) error {
	return s.db.WithContext(ctx).Delete(&entity.ProviderStorage{}, id).Error
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

// listRoots returns distinct (provider_type, instance_name) pairs with file count.
func (s *store) listRoots(ctx context.Context) ([]RootInfo, error) {
	var out []RootInfo
	err := s.db.WithContext(ctx).
		Model(&entity.ProviderStorage{}).
		Select("provider_type, instance_name, COUNT(*) as file_count").
		Where("is_dir = ?", false).
		Group("provider_type, instance_name").
		Scan(&out).Error
	return out, err
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
