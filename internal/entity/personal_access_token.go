package entity

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// PersonalAccessToken is a static bearer credential a user generates
// from /profile/mcp. The plaintext token is shown to the user exactly
// once at creation time; only the SHA-256 hash is persisted so it can
// be looked up on incoming MCP requests but never reconstructed.
//
// Token wire format: "wick_pat_" + 32 hex chars. Last4 stores the last
// 4 characters of the random suffix so the UI can render a stable
// "wick_pat_****abcd" preview without keeping the secret around.
//
// LastUsedAt is best-effort — written by the MCP middleware on
// successful auth. RevokedAt nil means active; non-nil hides the row
// from active-token queries while keeping the audit trail intact.
type PersonalAccessToken struct {
	ID         string `gorm:"type:varchar(36);primaryKey"`
	UserID     string `gorm:"type:varchar(36);not null;index"`
	Name       string `gorm:"type:varchar(120);not null"`
	TokenHash  string `gorm:"type:varchar(64);not null;uniqueIndex"`
	Last4      string `gorm:"type:varchar(8);not null"`
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time `gorm:"index"`
}

func (t *PersonalAccessToken) BeforeCreate(tx *gorm.DB) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	return nil
}

// Masked returns the display form for list views: prefix + asterisks +
// last 4 characters. Never exposes the secret.
func (t *PersonalAccessToken) Masked() string {
	return "wick_pat_****" + t.Last4
}
