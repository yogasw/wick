package entity

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// PluginTestRequest is one ask to run a locally built connector plugin
// (`go run ./plugins/connector/<key> --run-op …`, or under dlv) against the
// config of a real connector instance. It starts pending; an admin approves it
// for a limited time on /admin/plugins/dev-mode, and only then can the requester
// fetch the instance's plaintext config. The row is the audit trail too:
// FetchCount and LastFetchAt record every config hand-out.
type PluginTestRequest struct {
	ID           string `gorm:"type:varchar(36);primaryKey"`
	UserID       string `gorm:"type:varchar(36);not null;index"`
	ConnectorKey string `gorm:"type:varchar(64);not null"`
	InstanceID   string `gorm:"type:varchar(36);not null"`
	// InstanceLabel is the label at request time, kept for the admin list.
	InstanceLabel string `gorm:"type:varchar(200)"`
	// Operation is the op the requester said they would run (informational).
	Operation string `gorm:"type:varchar(120)"`
	// Host is the machine/cwd the binary SAYS it runs on (self-reported).
	Host string `gorm:"type:varchar(300)"`
	// Machine is the runner's "<os user>@<hostname>" sent with the request
	// and again with every use; a different value is refused.
	Machine string `gorm:"type:varchar(200)"`
	// Credential is WHICH bearer filed the request: "pat:<token id>" for a
	// personal access token, "session:<session id>" for an agent session.
	// Every use must come with that same credential, so another token — of
	// the same user or anyone else — cannot ride on this approval; it has to
	// file (and get approved) its own request.
	Credential string `gorm:"type:varchar(80)"`
	// RequestIP is the client IP wick saw when the request was filed; every
	// use must come from the same IP. LastFetchIP is the one that last used
	// it — both observed by the server, unlike Host.
	RequestIP   string `gorm:"type:varchar(64)"`
	LastFetchIP string `gorm:"type:varchar(64)"`
	// SecretHash is sha256 of a random secret the requesting binary keeps in
	// a local file. Fetching the config needs that secret, so an approval
	// only works on the machine that asked — a leaked PAT alone is not enough.
	SecretHash string `gorm:"type:varchar(64)"`
	// Status: pending | approved | rejected | revoked. Expiry is ExpiresAt,
	// not a status, so an approved row simply stops working when time runs out.
	Status      string `gorm:"type:varchar(16);not null;index"`
	DecidedBy   string `gorm:"type:varchar(36)"`
	DecidedAt   *time.Time
	ExpiresAt   *time.Time
	FetchCount  int
	LastFetchAt *time.Time
	CreatedAt   time.Time
}

const (
	PluginTestPending  = "pending"
	PluginTestApproved = "approved"
	PluginTestRejected = "rejected"
	PluginTestRevoked  = "revoked"
)

func (r *PluginTestRequest) BeforeCreate(tx *gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	return nil
}

// Active reports whether the request is approved and not yet expired.
func (r *PluginTestRequest) Active(now time.Time) bool {
	return r.Status == PluginTestApproved && r.ExpiresAt != nil && now.Before(*r.ExpiresAt)
}
