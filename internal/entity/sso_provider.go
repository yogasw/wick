package entity

import "time"

// SSOProvider holds one OAuth/SSO provider's configuration. The
// callback URL is never stored — it's derived at runtime from
// app_variables.app_url + "/auth/callback".
type SSOProvider struct {
	ID           uint   `gorm:"primaryKey"`
	Provider     string `gorm:"uniqueIndex;type:varchar(32);not null"` // "google"
	ClientID     string `gorm:"type:varchar(255)"`
	ClientSecret string `gorm:"type:varchar(255)"`
	Enabled      bool   `gorm:"default:false"`
	// AllowedDomains is a comma-separated list of email domains allowed
	// to sign in through this provider (e.g. "abc.com,abc.net").
	// Empty string means no restriction — any email from the provider is
	// accepted. Matching is case-insensitive.
	AllowedDomains string `gorm:"type:text"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (SSOProvider) TableName() string { return "sso_providers" }

// SSOProviderGoogle is the provider key for Google OAuth.
const SSOProviderGoogle = "google"
