package entity

import "time"

// Config is one row in the runtime-editable KV config table (legacy
// name: `app_variables`). Wick reconciles two sources into this table
// at boot:
//   - app-level defaults declared in internal/configs/spec.go
//     (Owner == "", for session_secret / app_url / app_name)
//   - per-module Config structs passed to app.RegisterTool. Framework
//     reflects the typed struct + `wick:"..."` tags into rows via
//     StructToConfigs, with Owner == Meta.Key stamped at boot.
//
// The composite primary key (Owner, Key) lets the same Key repeat
// across scopes without collision (every tool can have its own
// "api_key", for example).
//
// Type drives which widget the admin UI renders:
//
//	text (default), textarea, dropdown, number, checkbox,
//	email, url, color, date, datetime,
//	kvlist (editable table; Options = pipe-separated column names)
//
// Options is pipe-separated (`a|b|c`). For "dropdown" it holds option
// values; for "kvlist" it holds column names (e.g. "id|name"). Value
// for kvlist rows is a JSON array of string-keyed objects.
type Config struct {
	Owner         string `gorm:"primaryKey;type:varchar(64);default:''"`
	Key           string `gorm:"primaryKey;type:varchar(64)"`
	Value         string `gorm:"type:text"`
	Type          string `gorm:"type:varchar(16);default:'text'"`
	Options       string `gorm:"type:text"`
	IsSecret      bool   `gorm:"default:false"`
	CanRegenerate bool   `gorm:"default:false"`
	Locked        bool   `gorm:"default:false"`
	Required      bool   `gorm:"default:false"`
	Description   string `gorm:"type:text"`
	UpdatedAt     time.Time
}

func (Config) TableName() string { return "configs" }
