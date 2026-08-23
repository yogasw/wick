// Package channelidentity records which chat-channel accounts belong to which
// wick user, and answers "where can I reach this person".
//
// It is the missing half of channel auto-registration: matching a Slack sender
// to a wick user told us WHO they are, but nothing remembered HOW to reach them
// afterwards. Notifications (a new registration to approve, an approval to
// announce) need exactly that.
package channelidentity

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/yogasw/wick/internal/entity"
)

// Store persists channel identities.
type Store struct {
	db  *gorm.DB
	now func() time.Time
}

// NewStore builds a Store over db.
func NewStore(db *gorm.DB) *Store {
	return &Store{db: db, now: time.Now}
}

// Link records (or refreshes) the identity behind a channel sender.
//
// Upserts on channel+instance+external id, so a returning sender updates their
// row rather than accumulating duplicates — this runs on every resolved
// message, not just the first.
//
// A re-link deliberately does NOT clear DisabledAt: if a user paused a
// connection, messaging from it again must not silently re-enable delivery.
// Only an explicit Resume does that.
func (s *Store) Link(ctx context.Context, id entity.UserChannelIdentity) error {
	id.ChannelType = strings.TrimSpace(id.ChannelType)
	id.InstanceKey = strings.TrimSpace(id.InstanceKey)
	id.ExternalUserID = strings.TrimSpace(id.ExternalUserID)
	if id.UserID == "" || id.ChannelType == "" || id.ExternalUserID == "" {
		return nil // nothing identifiable to record
	}
	if id.InstanceKey == "" {
		// Never leave this blank: an empty instance key would collide across
		// two bots and could route a notification to the wrong workspace.
		id.InstanceKey = "default"
	}
	now := s.now()
	id.LastSeenAt = &now

	return s.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "channel_type"}, {Name: "instance_key"}, {Name: "external_user_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"user_id", "display_name", "email_at_link", "last_seen_at", "updated_at",
		}),
	}).Create(&id).Error
}

// identityFor builds a link row. Kept here so every caller stamps the same
// fields and no path forgets one.
func identityFor(userID, channelType, instanceKey, externalUserID, displayName string) entity.UserChannelIdentity {
	return entity.UserChannelIdentity{
		UserID:         userID,
		ChannelType:    channelType,
		InstanceKey:    instanceKey,
		ExternalUserID: externalUserID,
		DisplayName:    displayName,
		// No EmailAtLink: this path exists precisely because the channel
		// reports no email, and writing the synthetic placeholder here would
		// dress a made-up address up as the one it matched on.
	}
}

// FindUserID resolves an existing link from the channel account itself.
//
// This is the lookup that must run BEFORE an email match: the external account
// id is stable, while a channel email can be edited. Looking up by email alone
// would miss after such an edit and register a second wick account for someone
// who already has one.
//
// A paused connection still resolves — pausing stops NOTIFICATIONS, it does not
// unlink the account or revoke the person's access.
func (s *Store) FindUserID(ctx context.Context, channelType, instanceKey, externalUserID string) (string, bool) {
	channelType = strings.TrimSpace(channelType)
	externalUserID = strings.TrimSpace(externalUserID)
	if channelType == "" || externalUserID == "" {
		return "", false
	}
	if instanceKey = strings.TrimSpace(instanceKey); instanceKey == "" {
		instanceKey = "default"
	}
	var row entity.UserChannelIdentity
	err := s.db.WithContext(ctx).
		Where("channel_type = ? AND instance_key = ? AND external_user_id = ?",
			channelType, instanceKey, externalUserID).
		First(&row).Error
	if err != nil || row.UserID == "" {
		return "", false
	}
	return row.UserID, true
}

// ListForUser returns a user's channel connections, newest first.
func (s *Store) ListForUser(ctx context.Context, userID string) ([]entity.UserChannelIdentity, error) {
	var out []entity.UserChannelIdentity
	if userID == "" {
		return out, nil
	}
	err := s.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("channel_type asc, instance_key asc").
		Find(&out).Error
	return out, err
}

// ActiveForUser returns only the connections that may receive delivery, i.e.
// those not paused. Used by the notifier so a pause takes effect at SEND time
// rather than only in the UI.
func (s *Store) ActiveForUser(ctx context.Context, userID string) ([]entity.UserChannelIdentity, error) {
	var out []entity.UserChannelIdentity
	if userID == "" {
		return out, nil
	}
	err := s.db.WithContext(ctx).
		Where("user_id = ? AND disabled_at IS NULL", userID).
		Order("channel_type asc, instance_key asc").
		Find(&out).Error
	return out, err
}

// SetPaused pauses or resumes delivery to one connection. userID is required
// and scopes the update, so a caller cannot pause a row belonging to somebody
// else by guessing its id.
func (s *Store) SetPaused(ctx context.Context, userID, id string, paused bool) error {
	if userID == "" || id == "" {
		return nil
	}
	var disabledAt *time.Time
	if paused {
		now := s.now()
		disabledAt = &now
	}
	return s.db.WithContext(ctx).
		Model(&entity.UserChannelIdentity{}).
		Where("id = ? AND user_id = ?", id, userID).
		Update("disabled_at", disabledAt).Error
}
