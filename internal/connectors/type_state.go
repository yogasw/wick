package connectors

import (
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/yogasw/wick/internal/entity"
)

// typeStateStore reads and writes the connector TYPE enable/disable overlay
// (entity.ConnectorState). It is the type-level switch the manager header
// kebab flips — distinct from the per-instance row flag (entity.Connector.
// Disabled) and from the plugin registration overlay (plugin.StateStore).
//
// A nil store (or nil db) reports every type as enabled, so the Service can be
// constructed in tests without a DB and the switch simply becomes a no-op.
type typeStateStore struct{ db *gorm.DB }

// newTypeStateStore wraps db. nil db -> Enabled always true.
func newTypeStateStore(db *gorm.DB) *typeStateStore { return &typeStateStore{db: db} }

// Enabled reports whether the connector type key is enabled. Missing row or any
// read error -> true (default-on; never hide a connector on a transient error).
func (s *typeStateStore) Enabled(key string) bool {
	if s == nil || s.db == nil {
		return true
	}
	var st entity.ConnectorState
	if err := s.db.Where("key = ?", key).First(&st).Error; err != nil {
		return true
	}
	return st.Enabled
}

// SetEnabled upserts the overlay row for key. A map literal is used so gorm
// writes the literal enabled value; a struct would let the `default:true` tag
// override a zero-value false on insert.
func (s *typeStateStore) SetEnabled(key string, enabled bool) error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Model(&entity.ConnectorState{}).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"enabled", "updated_at"}),
	}).Create(map[string]interface{}{
		"key":        key,
		"enabled":    enabled,
		"updated_at": time.Now(),
	}).Error
}

// disabledKeys returns the set of type keys explicitly disabled (Enabled=false).
// Used by the manager list to badge them without an N+1 lookup. nil store -> empty.
func (s *typeStateStore) disabledKeys() map[string]bool {
	out := map[string]bool{}
	if s == nil || s.db == nil {
		return out
	}
	var rows []entity.ConnectorState
	if err := s.db.Where("enabled = ?", false).Find(&rows).Error; err != nil {
		return out
	}
	for _, r := range rows {
		out[r.Key] = true
	}
	return out
}
