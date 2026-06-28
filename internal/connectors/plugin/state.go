package plugin

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/yogasw/wick/internal/entity"
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
