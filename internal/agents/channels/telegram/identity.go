package telegram

import (
	"context"
	"errors"
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
)

// identity.go maps an inbound Telegram sender to the wick user the agent
// should act as, using the same resolver, the same auto-register switch, and
// the same approval gate as Slack.
//
// The join key is the difference. Slack's users.info reports a real email;
// the Telegram Bot API reports none — there is no email field on a Telegram
// user at all, at any scope. So the sender's numeric id is turned into a
// reserved-domain stand-in (agentchannels.SyntheticEmail) and everything
// downstream behaves as it does for Slack: an unknown sender becomes a
// PENDING account an admin has to approve, and an unapproved one is refused
// rather than quietly run as the channel owner.
//
// The stand-in is a lookup key, never a contact address. An admin who later
// merges the placeholder into the person's real account keeps the link
// working, because the channel-identity link is keyed on the numeric id
// rather than on the address.

// senderIdentityFor builds the shared identity shape from a Telegram message.
// Returns ok=false when there is no sender to resolve — a channel post, or a
// message from another bot.
func senderIdentityFor(msg *tgbotapi.Message) (agentchannels.SenderIdentity, bool) {
	if msg == nil || msg.From == nil {
		return agentchannels.SenderIdentity{}, false
	}
	s := senderFor(msg, "")
	if s == nil {
		return agentchannels.SenderIdentity{}, false
	}
	return agentchannels.SenderIdentity{
		ExternalUserID: s.ID,
		Email:          agentchannels.SyntheticEmail(s.ID),
		Name:           s.Name,
		IsBot:          msg.From.IsBot,
	}, true
}

// resolveSessionOwner picks the wick user a Telegram message runs as.
//
// Returns ok=false when identity mapping is not wired, which keeps the
// pre-existing behaviour: the turn runs as the channel owner. That fallback
// is deliberate — an install that never configured identity must not have its
// Telegram bot stop working.
func (t *Channel) resolveSessionOwner(msg *tgbotapi.Message) (string, bool) {
	t.mu.Lock()
	users := t.users
	instanceKey := t.sessionPrefix
	t.mu.Unlock()

	if users == nil {
		return "", false
	}
	u, ok := senderIdentityFor(msg)
	if !ok {
		return "", false
	}
	id, err := agentchannels.ResolveWickUser(
		context.Background(), users, u,
		users.AutoRegisterEnabled(context.Background()),
		"telegram", instanceKey,
	)
	if err != nil || id == "" {
		return "", false
	}
	// Record the link only once the sender is fully cleared, so a refused
	// account does not leave a connection an admin could mistake for working
	// access.
	users.RecordIdentity(context.Background(), id, u.ExternalUserID, u.Name, u.Email)
	return id, true
}

// checkSenderIdentity resolves the sender and returns the reply to post when
// they cannot be mapped to a usable wick account. Empty string = allowed.
//
// Split from resolveSessionOwner so the gate runs BEFORE the agent is
// spawned: refusing after a spawn would already have run a turn under the
// wrong identity, which is the thing this exists to prevent.
func (t *Channel) checkSenderIdentity(msg *tgbotapi.Message) string {
	t.mu.Lock()
	users := t.users
	instanceKey := t.sessionPrefix
	t.mu.Unlock()

	if users == nil {
		return "" // identity mapping not wired: keep legacy behaviour
	}
	u, ok := senderIdentityFor(msg)
	if !ok {
		return ""
	}
	autoRegister := users.AutoRegisterEnabled(context.Background())
	if _, err := agentchannels.ResolveWickUser(
		context.Background(), users, u, autoRegister, "telegram", instanceKey,
	); err != nil {
		return identityErrorMessage(err, autoRegister)
	}
	return ""
}

// identityErrorMessage renders the reply for a failed identity resolution.
// Phrased as the next action to take, because the person reading it in
// Telegram is the one who has to unblock it.
//
// Deliberately never names the synthetic address: it is an internal lookup
// key, and showing someone "8812@telegram.local" invites them to treat it as
// an inbox or to ask an admin to email it.
func identityErrorMessage(err error, autoRegister bool) string {
	switch {
	case errors.Is(err, agentchannels.ErrPendingApproval):
		return "⏳ Your account is waiting for approval.\n" +
			"An admin has to approve you under Admin → Users, then just message me again."
	case errors.Is(err, agentchannels.ErrToolAccessDenied):
		return "🔒 Your account is approved, but it has not been granted access to Agents.\n" +
			"Ask an admin to grant it under Admin → Users.\n" +
			"This is the same permission the wick dashboard checks — messaging me does not bypass it."
	case errors.Is(err, agentchannels.ErrNoAccount):
		if autoRegister {
			return "⚠️ I could not create a wick account for you. Ask an admin to invite you."
		}
		return "⚠️ You do not have a wick account yet. Ask an admin to invite you, " +
			"or to enable Auto-register under Agents settings."
	case errors.Is(err, agentchannels.ErrEmailRequired):
		return "⚠️ I could not work out which wick account to act as, so I did not run this. " +
			"Bots cannot be mapped to a wick account."
	default:
		return fmt.Sprintf("⚠️ Could not resolve your wick identity: %v", err)
	}
}
