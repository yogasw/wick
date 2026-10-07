package plugin

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/yogasw/wick/internal/entity"
	wickplugin "github.com/yogasw/wick/pkg/plugin"
)

// StateStore reads and writes the plugin enable/disable overlay.
type StateStore struct{ db *gorm.DB }

// NewStateStore wraps db. A nil db yields a store whose Enabled defaults to true.
func NewStateStore(db *gorm.DB) *StateStore { return &StateStore{db: db} }

// Enabled reports whether key may be registered/spawned. Missing row or any
// error -> true (default-on; never hide a plugin because of a read error).
func (s *StateStore) Enabled(key string) bool {
	if s == nil || s.db == nil {
		return true
	}
	var st entity.PluginState
	if err := s.db.Where("key = ?", key).First(&st).Error; err != nil {
		return true
	}
	return st.Enabled
}

// SetEnabled upserts the overlay row for key. A map is used so gorm writes the
// literal enabled value; a struct would let the `default:true` tag override a
// zero-value false on insert.
func (s *StateStore) SetEnabled(key string, enabled bool) error {
	return s.db.Model(&entity.PluginState{}).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"enabled", "updated_at"}),
	}).Create(map[string]interface{}{
		"key":        key,
		"enabled":    enabled,
		"updated_at": time.Now(),
	}).Error
}

// List returns key -> enabled for all overlay rows.
func (s *StateStore) List() (map[string]bool, error) {
	var rows []entity.PluginState
	if err := s.db.Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		out[r.Key] = r.Enabled
	}
	return out, nil
}

// All returns every overlay row (origin, source, health, versions).
func (s *StateStore) All() ([]entity.PluginState, error) {
	var rows []entity.PluginState
	if s == nil || s.db == nil {
		return rows, nil
	}
	err := s.db.Find(&rows).Error
	return rows, err
}

// Record upserts the kind + installed version for key without touching the
// enable flag (a new row starts enabled).
func (s *StateStore) Record(key, kind, version string) error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Model(&entity.PluginState{}).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"kind", "installed_version", "updated_at"}),
	}).Create(map[string]interface{}{
		"key":               key,
		"enabled":           true,
		"kind":              wickplugin.NormalizeKind(kind),
		"installed_version": version,
		"updated_at":        time.Now(),
	}).Error
}

// Plugin origins recorded in PluginState.Origin. "" means not recorded.
const (
	OriginOfficial = "official"
	OriginSource   = "source"
	OriginURLZip   = "url-zip"
	OriginUpload   = "upload"
)

// SetOrigin records how key was installed. Call after Record so the row
// exists.
func (s *StateStore) SetOrigin(key, origin string) error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Model(&entity.PluginState{}).Where("key = ?", key).Update("origin", origin).Error
}

// ClearInstalled forgets the installed and available versions of key after
// an uninstall, so Check updates stops reporting it. The row itself stays
// (enable flag, origin, source link) and a reinstall records a version again.
func (s *StateStore) ClearInstalled(key string) error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Model(&entity.PluginState{}).Where("key = ?", key).
		Updates(map[string]any{"installed_version": "", "available_version": ""}).Error
}

// Get returns the overlay row for key; ok=false when there is none.
func (s *StateStore) Get(key string) (entity.PluginState, bool) {
	var st entity.PluginState
	if s == nil || s.db == nil {
		return st, false
	}
	if err := s.db.Where("key = ?", key).First(&st).Error; err != nil {
		return st, false
	}
	return st, true
}
