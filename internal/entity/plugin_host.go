package entity

import "time"

// PluginHostPermission is the admin overlay on a plugin's Host API access,
// next to PluginState.Enabled. Item "" is the decision for the whole scope;
// any other item (a table, "GET /api/x", "connector.op") blocks or allows
// that one declared item. Scope "*" with item "*" is "Block all host
// access" for the plugin. A missing row means the scope's default.
type PluginHostPermission struct {
	PluginKey string `gorm:"primaryKey;type:varchar(100)"`
	Scope     string `gorm:"primaryKey;type:varchar(100)"`
	Item      string `gorm:"primaryKey;type:varchar(255)"`
	Allowed   bool
	UpdatedBy string `gorm:"type:varchar(36)"`
	UpdatedAt time.Time
}

// PluginHostCall is one Host API call a plugin made, kept for the admin
// "Access to wick" page. Rows older than the retention window are purged.
// Note never holds a secret or a full argument dump.
type PluginHostCall struct {
	ID         uint      `gorm:"primaryKey"`
	Ts         time.Time `gorm:"index;index:idx_plugin_host_calls_plugin_ts,priority:2"`
	PluginKey  string    `gorm:"index:idx_plugin_host_calls_plugin_ts,priority:1;type:varchar(100)"`
	Kind       string    `gorm:"type:varchar(20)"`
	RPC        string    `gorm:"column:rpc;type:varchar(64)"`
	Scope      string    `gorm:"type:varchar(100)"`
	Item       string    `gorm:"type:varchar(255)"`
	UserID     string    `gorm:"type:varchar(36)"`
	Source     string    `gorm:"type:varchar(16)"`
	OK         bool      `gorm:"column:ok"`
	Code       string    `gorm:"type:varchar(32)"`
	DurationMs int64
	Note       string `gorm:"type:text"`
}
