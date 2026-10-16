package hostapi

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
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

// ListCalls implements CallReader: newest first, with the total that
// matched the filter for paging.
func (s *GormStore) ListCalls(ctx context.Context, f CallFilter) ([]entity.PluginHostCall, int64, error) {
	q := s.db.WithContext(ctx).Model(&entity.PluginHostCall{})
	if !f.Since.IsZero() {
		q = q.Where("ts >= ?", f.Since)
	}
	if f.PluginKey != "" {
		q = q.Where("plugin_key = ?", f.PluginKey)
	}
	if f.FailedOnly {
		q = q.Where("ok = ?", false)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []entity.PluginHostCall
	err := q.Order("ts DESC").Order("id DESC").Offset(f.Offset).Limit(f.Limit).Find(&rows).Error
	return rows, total, err
}

// CallStats implements CallReader: one row per (plugin, scope) with calls
// since since. The last call time is read back by id so no database has to
// scan MAX(ts) into a time.
func (s *GormStore) CallStats(ctx context.Context, since time.Time) ([]CallStat, error) {
	var agg []struct {
		PluginKey string
		Scope     string
		Calls     int64
		Failed    int64
		Denied    int64
		LastID    uint
	}
	err := s.db.WithContext(ctx).Model(&entity.PluginHostCall{}).
		Select("plugin_key, scope, COUNT(*) AS calls, SUM(CASE WHEN ok THEN 0 ELSE 1 END) AS failed, "+
			"SUM(CASE WHEN code = ? THEN 1 ELSE 0 END) AS denied, MAX(id) AS last_id", codes.PermissionDenied.String()).
		Where("ts >= ?", since).Group("plugin_key, scope").Scan(&agg).Error
	if err != nil || len(agg) == 0 {
		return nil, err
	}
	ids := make([]uint, 0, len(agg))
	for _, a := range agg {
		ids = append(ids, a.LastID)
	}
	var last []entity.PluginHostCall
	if err := s.db.WithContext(ctx).Select("id, ts").Where("id IN ?", ids).Find(&last).Error; err != nil {
		return nil, err
	}
	at := make(map[uint]time.Time, len(last))
	for _, l := range last {
		at[l.ID] = l.Ts
	}
	out := make([]CallStat, 0, len(agg))
	for _, a := range agg {
		out = append(out, CallStat{PluginKey: a.PluginKey, Scope: a.Scope, Calls: a.Calls, Failed: a.Failed, Denied: a.Denied, Last: at[a.LastID]})
	}
	return out, nil
}
