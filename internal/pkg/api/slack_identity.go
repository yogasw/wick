package api

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/channelidentity"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// slackUserResolver adapts login.Service to the Slack channel's identity
// port, mapping a Slack sender's email to the wick user whose access the
// agent should run with.
//
// Kept as an adapter rather than handing the channel a *login.Service so the
// channel depends on the two calls it needs, not on the whole auth surface.
type slackUserResolver struct {
	users *login.Service
	// autoRegister reads the INSTALL-level switch (Agents settings), not this
	// channel's own row. Channel rows are per-owner, so a per-channel switch
	// would let any user who adds their own bot create wick accounts. Read live
	// so toggling it takes effect without a restart.
	autoRegister func() bool
	// identities records which Slack account belongs to which wick user, so a
	// notification later has somewhere to go. Without it the mapping is
	// recomputed per message and forgotten again.
	identities *channelidentity.Store
	// notifier announces a fresh registration to admins. nil = no notices.
	notifier *channelidentity.Notifier
	// instanceKey names WHICH Slack bot/workspace this resolver serves, so a
	// recorded identity can be messaged back through the same one.
	instanceKey string
}

// FindByChannelIdentity resolves an existing link from the Slack account id,
// which survives an email change. Checked before the email lookup so a renamed
// Slack email cannot mint a duplicate wick account for the same person.
func (r slackUserResolver) FindByChannelIdentity(ctx context.Context, channelType, instanceKey, externalUserID string) (string, bool) {
	if r.identities == nil {
		return "", false
	}
	return r.identities.FindUserID(ctx, channelType, instanceKey, externalUserID)
}

func (r slackUserResolver) FindByEmail(ctx context.Context, email string) (string, bool) {
	if r.users == nil {
		return "", false
	}
	return r.users.FindUserIDByEmail(ctx, email)
}

// RegisterFromChannel creates an UNAPPROVED wick account for a Slack sender
// whose email has none. The account exists so an admin can approve it in one
// click, but it can do nothing until they do — Slack vouches that the address
// is on the workspace, not that this sender controls it.
func (r slackUserResolver) RegisterFromChannel(ctx context.Context, email, name, source string) (string, error) {
	if r.users == nil {
		return "", nil
	}
	id, err := r.users.RegisterChannelUser(ctx, email, name, source)
	if err != nil {
		log.Warn().Err(err).Str("source", source).
			Msg("channel identity: auto-register failed")
		return "", err
	}
	log.Info().Str("source", source).Str("user_id", id).
		Msg("channel identity: registered pending user, awaiting admin approval")

	// Tell the admins there is something to approve. Without this the account
	// sits pending until somebody happens to open the users page, and the
	// sender is left waiting on a step nobody knows about.
	//
	// Best-effort: the account already exists, so a failed notice must not
	// turn into a failed registration.
	if r.notifier != nil {
		if u, uerr := r.users.GetUserByID(ctx, id); uerr == nil && u != nil {
			r.notifier.NotifyAdminsNewUser(ctx, u, source)
		}
	}
	return id, nil
}

// RecordIdentity remembers that a Slack account maps to a wick user, so later
// notices can be delivered back through the same bot.
//
// Called on every resolved message rather than only the first: it refreshes
// last-seen and repairs a row whose display name changed, and the upsert keeps
// that from accumulating duplicates.
func (r slackUserResolver) RecordIdentity(ctx context.Context, wickUserID, externalUserID, displayName, email string) {
	if r.identities == nil || wickUserID == "" || externalUserID == "" {
		return
	}
	if err := r.identities.Link(ctx, entity.UserChannelIdentity{
		UserID:         wickUserID,
		ChannelType:    "slack",
		InstanceKey:    r.instanceKey,
		ExternalUserID: externalUserID,
		DisplayName:    displayName,
		EmailAtLink:    email,
	}); err != nil {
		log.Warn().Err(err).Str("user_id", wickUserID).
			Msg("channel identity: failed to record slack identity")
	}
}

// CanUseAgents applies the same gate the dashboard applies to /tools/agents,
// so Slack is not a way around it. Slack is a second door onto one tool: a
// user blocked in the web UI must be blocked when they message the bot.
//
// CanAccessTool reads the caller's filter tags from the context, so the tags
// are loaded and stamped here — without that the user would look untagged and
// pass any tag-gated tool.
func (r slackUserResolver) CanUseAgents(ctx context.Context, wickUserID string) bool {
	if r.users == nil || wickUserID == "" {
		return false
	}
	u, err := r.users.GetUserByID(ctx, wickUserID)
	if err != nil || u == nil {
		return false
	}
	ctx = login.WithUser(ctx, u, r.users.GetUserFilterTagIDs(ctx, u.ID))
	return r.users.CanAccessTool(ctx, u, "/tools/agents", entity.VisibilityPrivate)
}

// IsApproved reports whether an admin has approved this account. Kept separate
// from CanUseAgents so a pending sender is told to wait for approval instead of
// being told they lack a permission nobody could have granted yet.
func (r slackUserResolver) IsApproved(ctx context.Context, wickUserID string) bool {
	if r.users == nil || wickUserID == "" {
		return false
	}
	u, err := r.users.GetUserByID(ctx, wickUserID)
	return err == nil && u != nil && u.Approved
}

// channelSenderFunc adapts Registry.SendDirect to channelidentity.ChannelSender
// so the notifier depends on the one call it needs rather than the registry.
type channelSenderFunc func(ctx context.Context, channelType, instanceKey, externalUserID, text string) error

func (f channelSenderFunc) SendDirect(ctx context.Context, channelType, instanceKey, externalUserID, text string) error {
	return f(ctx, channelType, instanceKey, externalUserID, text)
}

// channelAccountCreator lets an email-less channel (Telegram) record a sender as
// its own unapproved account, to be merged into the real one by an admin.
type channelAccountCreator struct {
	users *login.Service
}

// CreateForChannel reuses the same unapproved-account path Slack uses. The
// email passed in is a synthetic placeholder, which is why nothing here treats
// it as contactable.
func (c channelAccountCreator) CreateForChannel(ctx context.Context, email, name, source string) (string, error) {
	if c.users == nil {
		return "", nil
	}
	return c.users.RegisterChannelUser(ctx, email, name, source)
}

// AutoRegisterEnabled reports whether an unmatched sender may get an account.
// Defaults to false when unwired: creating accounts is the permissive choice, so
// a missing dependency must fall to the safe side.
func (r slackUserResolver) AutoRegisterEnabled(context.Context) bool {
	return r.autoRegister != nil && r.autoRegister()
}
