package entity

import "time"

// ConnectorState is the DB overlay for a connector TYPE's enable/disable
// switch — distinct from the per-instance entity.Connector.Disabled row flag.
// It applies to ANY connector key, built-in or plugin: a missing row means
// enabled (default-on), Enabled=false hides the whole connector type from the
// LLM surface (every instance + every operation) while the manager UI still
// shows it with a "Disabled" badge so it can be turned back on.
//
// Note the difference from entity.PluginState (internal/entity/plugin.go),
// which gates plugin registration/spawn at the reloader level for plugins
// only. ConnectorState is a type-level visibility switch for all connectors.
type ConnectorState struct {
	Key       string `gorm:"primaryKey"`
	Enabled   bool   `gorm:"default:true"`
	UpdatedAt time.Time
}
