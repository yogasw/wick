package entity

import "time"

// PluginSource is a remote place plugins are installed and updated from: a
// plugins.json URL (or a direct .zip link, installed once) or a GitHub repo
// whose releases carry plugins.json + zip assets. Admin-managed; every column
// is additive under AutoMigrate.
type PluginSource struct {
	ID   string `gorm:"primaryKey"`
	Name string
	// Type is "url" or "github".
	Type  string
	URL   string
	Owner string
	Repo  string
	// Private repos download assets through the GitHub API with PAT.
	Private bool
	// PAT is the encrypted (wick_cenc_) GitHub token. Never returned by the
	// API; only sent to the GitHub API host.
	PAT string
	// PubKey pins the publisher's base64 ed25519 key: when set, every zip
	// from this source must carry a valid signature over its zip_sha256.
	PubKey string
	// KeyFilter is a comma-separated allow-list of plugin keys ("" = all).
	KeyFilter       string
	AllowPrerelease bool
	AutoUpdate      bool `gorm:"default:false"`
	PollMinutes     int  `gorm:"default:30"`
	Enabled         bool `gorm:"default:true"`
	// ETag + IndexJSON cache the last index so a 304 reuses it and the
	// Available list renders without a network call.
	ETag        string
	IndexJSON   string `gorm:"type:text"`
	LastCheckAt *time.Time
	LastStatus  string
	LastError   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// PluginAudit records one plugin admin action: who, which source and key,
// version before and after, and the verified zip hash.
type PluginAudit struct {
	ID          uint      `gorm:"primaryKey"`
	At          time.Time `gorm:"index"`
	Actor       string
	Action      string
	SourceID    string
	Key         string
	FromVersion string
	ToVersion   string
	ZipSHA256   string
	Detail      string
}
