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
	// AutoApproveDomains is a comma-separated list of email domains whose
	// accounts are approved on sign-in, without an admin. Empty = every new
	// account waits for an admin, as before. Auto-approval never grants the
	// admin role.
	AutoApproveDomains string `gorm:"type:text"`
	// AutoApproveTags is a comma-separated list of tag ids given to an
	// account at the moment it is auto-approved (by AutoApproveDomains, on
	// any door: Google sign-in or a chat channel). Empty = no tags, as before.
	// Applied once, on approval: an admin removing a tag later is not undone
	// by the next sign-in.
	AutoApproveTags string `gorm:"type:text"`
	// DisableAutoRegister stops this provider from creating accounts: an
	// email with no wick account yet is refused at sign-in instead of being
	// recorded as a pending user. False (the zero value) keeps the old
	// behaviour, so existing rows need no backfill.
	DisableAutoRegister bool `gorm:"default:false"`
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

func (SSOProvider) TableName() string { return "sso_providers" }

// SSOProviderGoogle is the provider key for Google OAuth.
const SSOProviderGoogle = "google"
