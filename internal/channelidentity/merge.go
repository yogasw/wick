package channelidentity

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// merge.go folds one wick account into another and moves its channel
// connections across.
//
// Why this is needed: only some channels report an email. Slack does (with the
// users:read.email scope), so a Slack sender can be matched to an existing wick
// account automatically. Telegram does not — it exposes a numeric id and a
// username, nothing that identifies a person across systems. So a Telegram
// sender necessarily starts as a SEPARATE account, and the only correct way to
// join it to the person's real account is for a human to say "these two are the
// same".
//
// That decision cannot be inferred. Matching on a display name would merge two
// different people who happen to share one, which is worse than leaving the
// accounts apart.

// ErrSameUser is returned when the source and target are the same account.
var ErrSameUser = errors.New("cannot merge an account into itself")

// ErrMergeIntoUnapproved is returned when the target is not approved. Merging
// into a pending account would move working connections onto an account that
// cannot act, which reads as a broken merge.
var ErrMergeIntoUnapproved = errors.New("target account is not approved")

// ErrMergeAdminSource is returned when the source is an admin or the owner.
//
// A merge DELETES the source account. Allowing that for an admin would turn
// "merge" into a way to remove another administrator, and would silently drop
// whatever that account was the sole owner of.
var ErrMergeAdminSource = errors.New("cannot merge away an admin or owner account")

// MergeResult reports what a merge moved, so the caller can tell the operator
// what actually happened rather than just "done".
type MergeResult struct {
	MovedConnections int
	// SkippedConnections counts links the target already had for the same
	// channel account. Keeping the target's own row is the safe choice: it is
	// the one whose pause state the user set.
	SkippedConnections int
}

// Merge moves every channel connection from sourceUserID to targetUserID and
// deletes the source account.
//
// Runs in one transaction: a half-finished merge would leave a person's
// connections split across two accounts, which is harder to reason about than
// either end state.
//
// The source account is deleted rather than left empty. An orphaned account
// with no connections is indistinguishable from a real pending user, so an
// admin would be left approving ghosts forever.
func (s *Store) Merge(ctx context.Context, sourceUserID, targetUserID string) (MergeResult, error) {
	var res MergeResult
	if sourceUserID == "" || targetUserID == "" {
		return res, errors.New("both source and target are required")
	}
	if sourceUserID == targetUserID {
		return res, ErrSameUser
	}

	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var source, target entity.User
		if err := tx.First(&source, "id = ?", sourceUserID).Error; err != nil {
			return fmt.Errorf("source account not found: %w", err)
		}
		if err := tx.First(&target, "id = ?", targetUserID).Error; err != nil {
			return fmt.Errorf("target account not found: %w", err)
		}
		if source.IsAdmin() {
			return ErrMergeAdminSource
		}
		if !target.Approved {
			return ErrMergeIntoUnapproved
		}

		var links []entity.UserChannelIdentity
		if err := tx.Where("user_id = ?", sourceUserID).Find(&links).Error; err != nil {
			return err
		}
		for _, l := range links {
			// Does the target already hold this exact channel account? Only
			// possible if it was linked from both sides; keep the target's row
			// because that is the one carrying the pause state they chose.
			var existing int64
			if err := tx.Model(&entity.UserChannelIdentity{}).
				Where("user_id = ? AND channel_type = ? AND instance_key = ? AND external_user_id = ?",
					targetUserID, l.ChannelType, l.InstanceKey, l.ExternalUserID).
				Count(&existing).Error; err != nil {
				return err
			}
			if existing > 0 {
				if err := tx.Delete(&entity.UserChannelIdentity{}, "id = ?", l.ID).Error; err != nil {
					return err
				}
				res.SkippedConnections++
				continue
			}
			if err := tx.Model(&entity.UserChannelIdentity{}).
				Where("id = ?", l.ID).
				Update("user_id", targetUserID).Error; err != nil {
				return err
			}
			res.MovedConnections++
		}

		// Drop the now-empty source so it stops showing up as a pending user.
		return tx.Delete(&entity.User{}, "id = ?", sourceUserID).Error
	})
	if err != nil {
		return MergeResult{}, err
	}
	return res, nil
}

// MergeCandidate is an account that looks mergeable: it arrived over a channel
// and has no email a match could have used.
type MergeCandidate struct {
	User        entity.User
	Connections []entity.UserChannelIdentity
}

// ListMergeCandidates returns accounts that exist only because their channel
// could not identify them — no email, so nothing to match on automatically.
//
// This is a SUGGESTION list, not a decision. It deliberately does not guess who
// each account belongs to: the only signal available is a display name, and
// merging two people who share a name is worse than leaving them apart.
func (s *Store) ListMergeCandidates(ctx context.Context) ([]MergeCandidate, error) {
	var users []entity.User
	err := s.db.WithContext(ctx).
		Where("(email IS NULL OR email = '' OR email LIKE ?) AND approved = ?", "%@"+placeholderEmailDomain, false).
		Order("created_at asc").
		Find(&users).Error
	if err != nil {
		return nil, err
	}
	out := make([]MergeCandidate, 0, len(users))
	for _, u := range users {
		links, err := s.ListForUser(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		if len(links) == 0 {
			continue // not a channel-created account
		}
		out = append(out, MergeCandidate{User: u, Connections: links})
	}
	return out, nil
}

// placeholderEmailDomain marks the synthetic address given to a channel account
// that reports no email. entity.User.Email is unique and NOT NULL, so such an
// account still needs a value; a reserved domain keeps it obviously fake and
// makes these rows findable.
const placeholderEmailDomain = "channel.local"

// PlaceholderEmail builds the synthetic address for a channel account with no
// real email, e.g. "telegram-12345@channel.local".
//
// Deliberately not a guessed real address: a made-up plausible email could
// later collide with the actual person's account and merge two identities
// without anyone deciding to.
func PlaceholderEmail(channelType, externalUserID string) string {
	return fmt.Sprintf("%s-%s@%s", channelType, externalUserID, placeholderEmailDomain)
}

// IsPlaceholderEmail reports whether an address is one wick invented, so the UI
// can show "no email" instead of a fake one.
func IsPlaceholderEmail(email string) bool {
	if email == "" {
		return true
	}
	return len(email) > len(placeholderEmailDomain)+1 &&
		email[len(email)-len(placeholderEmailDomain):] == placeholderEmailDomain
}
