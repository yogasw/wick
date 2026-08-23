package entity

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// UserChannelIdentity records that a wick user reaches wick through a specific
// account on a specific chat channel instance — "this Slack account in the acme
// workspace is this wick user".
//
// It exists because the identity was previously thrown away: the Slack path
// matched a sender's email to a wick user and dropped the Slack user ID, so
// nothing could later answer "where can I reach this person" or "which door did
// they come in through".
//
// Not stored in connector_accounts, despite that table also pairing a wick user
// with an external one: those rows are per connector INSTANCE and require an
// AccessToken (not null). A chat sender has no token — their identity comes from
// users.info, not an OAuth grant — so reusing it would mean writing empty
// strings into a column that means "credential".
type UserChannelIdentity struct {
	ID     string `gorm:"type:varchar(36);primaryKey"`
	UserID string `gorm:"type:varchar(36);not null;index"`
	User   User   `gorm:"foreignKey:UserID"`

	// ChannelType is the transport: "slack", "telegram", … Web is never
	// recorded — every wick user can already be reached there, so a row for it
	// would be noise in the UI.
	ChannelType string `gorm:"type:varchar(32);not null;index:idx_channel_identity_lookup,unique"`

	// InstanceKey identifies WHICH bot / workspace this identity belongs to.
	//
	// Required, not optional: wick supports several Slack instances at once
	// (per-owner bots, possibly in different workspaces). Without it "slack"
	// is ambiguous the moment a second bot exists, and a notification could be
	// delivered to the wrong workspace. Mirrors the channel registry's own
	// instance key.
	InstanceKey string `gorm:"type:varchar(128);not null;index:idx_channel_identity_lookup,unique"`

	// ExternalUserID is the channel-side user id (a Slack "U…", a Telegram
	// numeric id). Unique per channel+instance.
	ExternalUserID string `gorm:"type:varchar(255);not null;index:idx_channel_identity_lookup,unique"`

	// DisplayName is the channel-side name, kept for the UI so a row reads as
	// a person rather than an opaque id.
	DisplayName string `gorm:"type:varchar(255)"`

	// EmailAtLink is the email this identity was matched on. Kept because a
	// channel account's email can change later, and an admin debugging "why is
	// this linked to that user" needs to see what it matched at the time.
	EmailAtLink string `gorm:"type:varchar(320)"`

	// DisabledAt pauses delivery to this connection. Mirrors
	// PushSubscription.DisabledAt so "pause" means one thing across the UI.
	// Must be honoured at SEND time, not merely rendered — a pause button that
	// only greys a row is lying.
	DisabledAt *time.Time `gorm:"index"`

	LastSeenAt *time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

func (i *UserChannelIdentity) BeforeCreate(tx *gorm.DB) error {
	if i.ID == "" {
		i.ID = uuid.NewString()
	}
	return nil
}

// Paused reports whether delivery to this connection is currently suspended.
func (i *UserChannelIdentity) Paused() bool { return i.DisabledAt != nil }
