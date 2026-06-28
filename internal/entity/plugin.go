package entity

import "time"

// PluginState is the DB overlay for connector plugin enable/disable. A missing
// row means enabled (default-on); Enabled=false suppresses the plugin from
// registration and spawning. The on-disk scan stays the source of truth for
// which plugins exist.
type PluginState struct {
	Key       string `gorm:"primaryKey"`
	Enabled   bool   `gorm:"default:true"`
	UpdatedAt time.Time
}
