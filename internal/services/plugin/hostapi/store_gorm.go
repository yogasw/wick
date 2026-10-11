package hostapi

import (
	"context"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/yogasw/wick/internal/entity"
)

// GormStore keeps the permission overlay and the call audit in the wick
// database (plugin_host_permissions, plugin_host_calls).
type GormStore struct{ db *gorm.DB }

// NewGormStore wraps db.
func NewGormStore(db *gorm.DB) *GormStore { return &GormStore{db: db} }

// List implements PermissionStore.
func (s *GormStore) List(ctx context.Context, pluginKey string) ([]entity.PluginHostPermission, error) {
	var rows []entity.PluginHostPermission
	err := s.db.WithContext(ctx).Where("plugin_key = ?", pluginKey).Find(&rows).Error
	return rows, err
}

// Set implements PermissionStore. A map is written so a false Allowed is
// stored as such on insert.
func (s *GormStore) Set(ctx context.Context, p entity.PluginHostPermission) error {
	return s.db.WithContext(ctx).Model(&entity.PluginHostPermission{}).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "plugin_key"}, {Name: "scope"}, {Name: "item"}},
		DoUpdates: clause.AssignmentColumns([]string{"allowed", "updated_by", "updated_at"}),
	}).Create(map[string]any{
		"plugin_key": p.PluginKey, "scope": p.Scope, "item": p.Item,
		"allowed": p.Allowed, "updated_by": p.UpdatedBy, "updated_at": p.UpdatedAt,
	}).Error
}

// Delete implements PermissionStore.
func (s *GormStore) Delete(ctx context.Context, pluginKey, scope, item string) error {
	return s.db.WithContext(ctx).Where("plugin_key = ? AND scope = ? AND item = ?", pluginKey, scope, item).
		Delete(&entity.PluginHostPermission{}).Error
}

// WriteCalls implements AuditWriter.
func (s *GormStore) WriteCalls(ctx context.Context, calls []entity.PluginHostCall) error {
	if len(calls) == 0 {
		return nil
	}
	return s.db.WithContext(ctx).CreateInBatches(calls, 200).Error
}

// PurgeCallsBefore implements AuditWriter.
func (s *GormStore) PurgeCallsBefore(ctx context.Context, t time.Time) (int64, error) {
	res := s.db.WithContext(ctx).Where("ts < ?", t).Delete(&entity.PluginHostCall{})
	return res.RowsAffected, res.Error
}
