package entity

import "time"

// PluginState is the DB overlay for plugin enable/disable plus per-plugin
// bookkeeping (kind, installed/available version, last health). A missing
// row means enabled (default-on); Enabled=false suppresses the plugin from
// registration and spawning. The on-disk scan stays the source of truth for
// which plugins exist. Every column after Enabled is additive — AutoMigrate
// adds them to existing tables without touching old rows.
type PluginState struct {
	Key     string `gorm:"primaryKey"`
	Enabled bool   `gorm:"default:true"`
	// Kind is connector / tool / job / service. Old rows predate kinds and
	// read back as connector.
	Kind             string `gorm:"default:connector"`
	SourceID         string
	InstalledVersion string
	AvailableVersion string
	LastHealthAt     *time.Time
	LastHealthOK     bool
	LastHealthDetail string
	UpdatedAt        time.Time
}
