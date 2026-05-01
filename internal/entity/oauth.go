package entity

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// OAuthClient is one Dynamic Client Registration record (RFC 7591).
//
// MCP clients (Claude.ai web, Claude Desktop, Cursor) call POST
// /oauth/register without prior coordination, hand wick a name +
// redirect_uris, and receive back the ClientID. There is no client
// secret in this flow — every client is treated as a public client
// using PKCE per RFC 7636 (which the MCP authorization spec mandates).
//
// RedirectURIs is a JSON-encoded array of allowed redirect URIs;
// /oauth/authorize verifies the requested URI is one of them. We
// store as JSON to keep gorm migrations simple — the list is small
// and read whole every time.
//
// CreatedBy is non-nil only when the registration happened while a
// user was logged in (rare — DCR usually fires before any wick
// session exists). Useful for admin auditing.
type OAuthClient struct {
	ID           string `gorm:"type:varchar(36);primaryKey"`
	ClientID     string `gorm:"type:varchar(64);uniqueIndex;not null"`
	Name         string `gorm:"type:varchar(255)"`
	RedirectURIs string `gorm:"type:text;not null"` // JSON array
	CreatedBy    string `gorm:"type:varchar(36)"`
	CreatedAt    time.Time
}

func (c *OAuthClient) BeforeCreate(tx *gorm.DB) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	return nil
}

// OAuthAuthorizationCode is the short-lived PKCE authorization code
// minted at /oauth/authorize and consumed at /oauth/token.
//
// Code is the opaque string sent in the redirect query string. It's
// indexed for the lookup at /token but not unique — we let the
// database flag duplicates if two requests collide (statistically
// near impossible with 32 random bytes).
//
// CodeChallenge / Method come from the original /authorize request;
// /token verifies the client's code_verifier against them per RFC 7636.
//
// Used flips to true on first /token redemption to prevent replay.
// We keep the row for audit instead of deleting; PurgeExpired sweeps
// later.
type OAuthAuthorizationCode struct {
	ID                  string `gorm:"type:varchar(36);primaryKey"`
	Code                string `gorm:"type:varchar(64);uniqueIndex;not null"`
	ClientID            string `gorm:"type:varchar(64);index;not null"`
	UserID              string `gorm:"type:varchar(36);not null"`
	RedirectURI         string `gorm:"type:varchar(512);not null"`
	Scope               string `gorm:"type:varchar(255)"`
	CodeChallenge       string `gorm:"type:varchar(128);not null"`
	CodeChallengeMethod string `gorm:"type:varchar(10);not null"`
	Used                bool   `gorm:"default:false"`
	ExpiresAt           time.Time
	CreatedAt           time.Time
}

func (c *OAuthAuthorizationCode) BeforeCreate(tx *gorm.DB) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	return nil
}

// OAuthToken is one issued access or refresh token. Stored opaque
// (32 hex chars), hashed at rest just like PersonalAccessToken — the
// plaintext only crosses the wire on the /token response.
//
// Kind is "access" or "refresh". A code redemption mints both: the
// access has a short TTL (~1h), the refresh a long one (~30d) and is
// rotated on every use (RFC 6749 §6 + best-current-practice).
//
// ParentTokenID chains refresh-token rotation: when a refresh redeems,
// the new refresh row carries the previous row's ID here. RevokedAt
// stamps a row when its child is minted, so reuse of an old refresh
// is detectable (and the whole chain should be revoked — a sign the
// token was leaked).
type OAuthToken struct {
	ID            string `gorm:"type:varchar(36);primaryKey"`
	TokenHash     string `gorm:"type:varchar(64);uniqueIndex;not null"`
	Kind          string `gorm:"type:varchar(10);not null"` // access | refresh
	ClientID      string `gorm:"type:varchar(64);index;not null"`
	UserID        string `gorm:"type:varchar(36);index;not null"`
	Scope         string `gorm:"type:varchar(255)"`
	ParentTokenID *string `gorm:"type:varchar(36);index"` // refresh chain ancestor
	ExpiresAt     time.Time
	RevokedAt     *time.Time `gorm:"index"`
	LastUsedAt    *time.Time
	CreatedAt     time.Time
}

func (t *OAuthToken) BeforeCreate(tx *gorm.DB) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	return nil
}
