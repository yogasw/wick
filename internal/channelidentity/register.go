package channelidentity

import (
	"context"
	"strings"

	"github.com/rs/zerolog/log"
)

// register.go handles channels that cannot identify a sender.
//
// Slack reports an email (with users:read.email), so a Slack sender can be
// matched to an existing wick account. Telegram reports a numeric id and a
// username and nothing else — there is no field that identifies the same person
// in wick. So a Telegram sender cannot be matched; they can only be recorded as
// a NEW account, which an admin later merges into the person's real one.
//
// The alternative — matching on display name — is not on the table: two people
// sharing a name would silently become one account, which is worse than two
// accounts an admin can join deliberately.

// AccountCreator creates a wick account for a channel identity. Implemented by
// the server over login.Service.
type AccountCreator interface {
	// CreateForChannel makes an unapproved account with the given email and
	// name, returning its id. Must be idempotent on email.
	CreateForChannel(ctx context.Context, email, name, source string) (string, error)
}

// EmaillessResolver resolves senders on channels that report no email, by
// recording them as their own account.
type EmaillessResolver struct {
	Store    *Store
	Accounts AccountCreator
	// Notifier announces the new pending account to admins. nil = no notice,
	// which means nobody learns there is something to merge.
	Notifier *Notifier
}

// Resolve maps a sender to a wick user id, creating an account the first time.
//
// The account gets a PLACEHOLDER email (telegram-<id>@channel.local) because
// entity.User.Email is unique and NOT NULL. It is deliberately obviously fake:
// inventing a plausible real address could later collide with that person's
// actual account and join two identities with nobody deciding to.
//
// Returns the wick user id. The account is unapproved, so the caller's normal
// approval gate still refuses it — this records WHO is asking, it does not
// grant anything.
func (r *EmaillessResolver) Resolve(ctx context.Context, channelType, instanceKey, externalUserID, displayName string) (string, error) {
	channelType = strings.TrimSpace(channelType)
	externalUserID = strings.TrimSpace(externalUserID)
	if r == nil || r.Store == nil || r.Accounts == nil ||
		channelType == "" || externalUserID == "" {
		return "", nil
	}
	if instanceKey = strings.TrimSpace(instanceKey); instanceKey == "" {
		instanceKey = "default"
	}

	// An existing link is the only reliable way back to the same account, since
	// there is no email to look up. This is also what stops a second account
	// being created on every message.
	if id, ok := r.Store.FindUserID(ctx, channelType, instanceKey, externalUserID); ok {
		_ = r.touch(ctx, id, channelType, instanceKey, externalUserID, displayName)
		return id, nil
	}

	email := PlaceholderEmail(channelType, externalUserID)
	name := strings.TrimSpace(displayName)
	if name == "" {
		name = channelType + " user " + externalUserID
	}
	userID, err := r.Accounts.CreateForChannel(ctx, email, name, channelType)
	if err != nil {
		return "", err
	}
	if err := r.touch(ctx, userID, channelType, instanceKey, externalUserID, displayName); err != nil {
		return "", err
	}

	log.Info().
		Str("channel", channelType).
		Str("instance", instanceKey).
		Str("user_id", userID).
		Msg("channel identity: created placeholder account for an email-less channel; awaiting merge")

	// Tell the admins: this account exists only so it can be merged, and
	// nothing will happen until somebody does it.
	if r.Notifier != nil {
		r.Notifier.NotifyAdminsMergeNeeded(ctx, userID, name, channelType)
	}
	return userID, nil
}

func (r *EmaillessResolver) touch(ctx context.Context, userID, channelType, instanceKey, externalUserID, displayName string) error {
	return r.Store.Link(ctx, identityFor(userID, channelType, instanceKey, externalUserID, displayName))
}
