package entity

import "time"

// CredentialRequest is an agent asking a person for credentials (a staging
// login, an API key) without them ever passing through the chat. The agent
// files it with wick_ask_credentials and ends its turn; a card appears where
// the session lives (Slack thread, web); the person fills a form there and
// picks how long the values may live. The agent only ever receives reference
// tokens (wick_cred_…); a script resolves them at runtime through the
// session's CLI token.
type CredentialRequest struct {
	ID string `gorm:"type:varchar(36);primaryKey"`
	// SessionID is the conversation that asked; only it may resolve tokens.
	SessionID string `gorm:"type:text;not null;index:idx_cred_req_session"`
	// RequesterUserID is the wick user the session belongs to.
	RequesterUserID string `gorm:"type:text"`
	// AgentName is the asking agent's display name, for the card.
	AgentName string `gorm:"type:text"`
	// Title names what the credentials are for ("OC staging login").
	Title string `gorm:"type:text"`
	// Reason is the agent's own words, shown as its claim.
	Reason string `gorm:"type:text"`
	// Fields is the JSON list of requested fields ({key,label,secret,optional}).
	Fields string `gorm:"type:text;not null"`
	// SuggestTTL is the retention the agent proposed (once, 1h, 4h, 8h, 24h).
	SuggestTTL string `gorm:"type:varchar(8)"`
	// Status: pending → filled → expired; cancelled when withdrawn.
	Status string `gorm:"type:varchar(16);not null;index:idx_cred_req_status"`
	// Values is the filled map, encrypted at rest with the master key. Wiped
	// (set to "") once expired, used up or cancelled.
	Values string `gorm:"type:text"`
	// TTL is the retention the person picked.
	TTL string `gorm:"type:varchar(8)"`
	// Used is the JSON list of field keys already read, for TTL "once".
	Used string `gorm:"type:text"`
	// FilledBy / FilledByName: who filled it.
	FilledBy     string `gorm:"type:text"`
	FilledByName string `gorm:"type:text"`
	FilledAt     *time.Time
	// ExpiresAt is when the values stop resolving (nil while pending).
	ExpiresAt *time.Time `gorm:"index:idx_cred_req_expires"`
	// Origin is where the card was posted (JSON: slack channel, ts, thread).
	Origin    string    `gorm:"type:text"`
	CreatedAt time.Time `gorm:"not null"`
	UpdatedAt time.Time `gorm:"not null"`
}
