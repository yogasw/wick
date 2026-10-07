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
	Kind     string `gorm:"default:connector"`
	SourceID string
	// Origin is how the plugin got here: official (wick connector catalog),
	// source (a plugin source), url-zip (a one-off .zip link source) or
	// upload. Empty = not recorded (copied in by hand, CLI path/url install,
	// or installed before this column existed) — shown as local/unknown.
	Origin           string
	InstalledVersion string
	AvailableVersion string
	LastHealthAt     *time.Time
	LastHealthOK     bool
	LastHealthDetail string
	UpdatedAt        time.Time
}

// PluginReplacement marks that the data of a replaced key (OldKey, usually a
// built-in) was migrated onto the plugin that declared `replaces` (NewKey).
// The row is the idempotency marker: boot-time migration runs once per pair
// and an admin re-run must ask for it explicitly. Detail is a short summary
// of what moved — never a config value.
type PluginReplacement struct {
	OldKey     string `gorm:"primaryKey;type:varchar(100)"`
	NewKey     string `gorm:"primaryKey;type:varchar(100)"`
	Kind       string `gorm:"type:varchar(20)"`
	MigratedAt time.Time
	Detail     string `gorm:"type:text"`
}
