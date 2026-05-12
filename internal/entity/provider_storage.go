package entity

import "time"

// RootParentID is the sentinel parent_id value for root-level items (direct
// children of an instance root). Using 0 instead of NULL avoids SQLite's
// NULL-inequality issue in unique indexes.
const RootParentID = uint(0)

// ProviderStorage holds one synced file or folder entry for a provider instance.
// Mode "folder" produces multiple rows (one per file); mode "single"
// produces exactly one. content_hash gates writes — only changed files
// are re-upserted.
//
// Adjacency-list layout: each row knows its parent via ParentID (0 = root).
// Folder rows have IsDir=true, Content=nil, ContentHash="".
type ProviderStorage struct {
	ID            uint      `gorm:"primaryKey;autoIncrement"`
	ProviderType  string    `gorm:"type:varchar(32);not null;uniqueIndex:idx_provider_path"`
	InstanceName  string    `gorm:"type:varchar(128);not null;uniqueIndex:idx_provider_path"`
	RelPath       string    `gorm:"type:varchar(512);not null;uniqueIndex:idx_provider_path"` // relative to SyncPath
	ParentID      uint      `gorm:"default:0;index"`                                          // 0 = root (RootParentID)
	Name          string    `gorm:"type:varchar(512)"`                                        // basename only
	IsDir         bool      `gorm:"default:false"`
	Content       []byte    `gorm:"type:blob"`
	ContentHash   string    `gorm:"type:varchar(64);not null"` // SHA-256 hex; "" for dirs
	SyncedAt      time.Time `gorm:"not null"`
	RetentionDays int       `gorm:"not null;default:0"` // 0 = never purge
}

func (ProviderStorage) TableName() string { return "provider_storage" }

// ProviderStorageSource is one configured sync source per provider instance.
// Multiple sources can exist per instance (e.g. credentials folder + sessions folder).
// The Manager reads this table at boot (RestoreAll) and at Start (background tickers).
type ProviderStorageSource struct {
	ID              uint      `gorm:"primaryKey;autoIncrement"`
	ProviderType    string    `gorm:"type:varchar(32);not null;index:idx_source_provider"`
	InstanceName    string    `gorm:"type:varchar(128);not null;index:idx_source_provider"`
	Label           string    `gorm:"type:varchar(128);not null"` // e.g. "claude workspace", "credentials"
	SyncPath        string    `gorm:"type:varchar(1024);not null"`
	Mode            string    `gorm:"type:varchar(16);not null;default:'folder'"` // "folder" | "single"
	RetentionDays   int       `gorm:"not null;default:0"` // 0 = never purge
	Enabled         bool      `gorm:"not null;default:true"`
	CreatedAt       time.Time `gorm:"not null"`
	UpdatedAt       time.Time `gorm:"not null"`
}

func (ProviderStorageSource) TableName() string { return "provider_storage_sources" }
