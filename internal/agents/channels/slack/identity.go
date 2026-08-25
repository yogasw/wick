package slack

import (
	"context"
	"errors"
	"strings"

	slackgo "github.com/slack-go/slack"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
)

// identity.go maps an inbound Slack sender to the wick user whose identity
// the agent should act with.
//
// This matters because the session owner decides the MCP credential the
// spawned agent carries: get it wrong and the turn runs with someone else's
// connector access, or with the synthetic admin. Slack's user ID is not a
// wick identity, so the join has to happen on something both sides agree on
// — the email address.

// The identity contract lives in the parent package so Slack and Telegram
// resolve senders through exactly one implementation — a second copy is how
// one transport ends up quietly skipping the approval gate. Aliased here so
// this package's call sites stay readable.
type UserResolver = agentchannels.UserResolver

var (
	ErrEmailRequired    = agentchannels.ErrEmailRequired
	ErrNoAccount        = agentchannels.ErrNoAccount
	ErrToolAccessDenied = agentchannels.ErrToolAccessDenied
	ErrPendingApproval  = agentchannels.ErrPendingApproval
	ErrGuestNotAllowed  = agentchannels.ErrGuestNotAllowed
)

// SlackUser is the subset of users.info this package needs.
type SlackUser struct {
	Email     string
	Name      string
	IsBot     bool
	IsGuest   bool
	IsDeleted bool
}

// slackUserFrom converts a slack-go user into the local shape. Guest status
// covers both restriction levels Slack exposes.
func slackUserFrom(u *slackgo.User) SlackUser {
	if u == nil {
		return SlackUser{}
	}
	return SlackUser{
		Email:     strings.TrimSpace(u.Profile.Email),
		Name:      firstNonEmpty(u.RealName, u.Name),
		IsBot:     u.IsBot,
		IsGuest:   u.IsRestricted || u.IsUltraRestricted,
		IsDeleted: u.Deleted,
	}
}

// senderIdentityFrom adapts the slack-shaped user onto the shared contract.
func senderIdentityFrom(u SlackUser, slackUserID string) agentchannels.SenderIdentity {
	return agentchannels.SenderIdentity{
		ExternalUserID: slackUserID,
		Email:          u.Email,
		Name:           u.Name,
		IsBot:          u.IsBot,
		IsGuest:        u.IsGuest,
		IsDeleted:      u.IsDeleted,
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// identityErrorMessage renders the operator-facing reply for a failed
// identity resolution. Phrased as the next action to take, because the person
// reading it in Slack is the one who has to unblock it.
func identityErrorMessage(err error, autoRegister bool) string {
	switch {
	case errors.Is(err, ErrEmailRequired):
		return ":warning: *email is required* — I could not read your email from Slack, so I cannot tell which wick account to act as.\n" +
			"Ask an admin to add the `users:read.email` scope to the Slack app and reinstall it. " +
			"Bots and apps cannot be mapped to a wick account at all."
	case errors.Is(err, ErrPendingApproval):
		return ":hourglass: *Account pending approval* — I matched your Slack email to a wick " +
			"account, but no admin has approved it yet, so I cannot run anything for you.\n" +
			"Ask an admin to approve you under *Admin → Users*, then just message me again."
	case errors.Is(err, ErrToolAccessDenied):
		return ":lock: *No access to Agents* — your account is approved, but it has not been " +
			"granted access to the Agents tool.\n" +
			"Ask an admin to grant it under *Admin → Users*.\n" +
			"_This is the same permission the wick dashboard checks — messaging me does not bypass it._"
	case errors.Is(err, ErrGuestNotAllowed):
		return ":warning: Guest accounts cannot use this assistant. Ask an admin for a full workspace account."
	case errors.Is(err, ErrNoAccount):
		if autoRegister {
			return ":warning: I could not create a wick account for your email. Ask an admin to invite you."
		}
		return ":warning: No wick account matches your Slack email. Ask an admin to invite you, " +
			"or have them enable *Auto-register* on this Slack channel."
	default:
		return ":warning: Could not resolve your wick identity: " + err.Error()
	}
}

// resolveSessionOwner picks the wick user a NEW Slack-started session belongs
// to, so the agent runs with that person's connector access — the same
// identity they would get by opening the session in the web UI.
//
// Order matters. The email match is tried first because it is the identity
// that actually scopes access; the OAuth-connect mapping (wickUserIDFn) is a
// narrower fallback that only covers senders who already linked an account.
// Returning ok=false leaves the caller to fall back to the channel owner,
// which is the pre-existing shared-identity behaviour.
func (s *Channel) resolveSessionOwner(slackUserID string) (string, bool) {
	s.cfgMu.Lock()
	users := s.users
	byOAuth := s.wickUserIDFn
	api := s.api
	instanceKey := s.sessionPrefix
	s.cfgMu.Unlock()

	autoRegister := users != nil && users.AutoRegisterEnabled(context.Background())

	if users != nil && api != nil {
		u, err := api.GetUserInfo(slackUserID)
		if err == nil {
			su := slackUserFrom(u)
			id, rerr := agentchannels.ResolveWickUser(context.Background(), users, senderIdentityFrom(su, slackUserID), autoRegister, "slack", instanceKey)
			if rerr == nil && id != "" {
				// Record the link only once the sender is fully cleared, so a
				// refused account does not leave a connection an admin could
				// mistake for working access.
				users.RecordIdentity(context.Background(), id, slackUserID, su.Name, su.Email)
				return id, true
			}
			// A refusal is deliberate and must not silently fall through to a
			// shared identity: the caller reports it to the sender instead.
			if rerr != nil {
				return "", false
			}
		}
	}
	if byOAuth != nil {
		if id, ok := byOAuth(context.Background(), slackUserID); ok {
			return id, true
		}
	}
	return "", false
}

// checkSenderIdentity resolves the sender and returns the reply to post when
// they cannot be mapped to a wick account. Empty string = allowed.
//
// Split from resolveSessionOwner so the gate runs BEFORE the agent is spawned:
// refusing after a spawn would already have run a turn under the wrong
// identity, which is the thing this exists to prevent.
func (s *Channel) checkSenderIdentity(slackUserID string) string {
	s.cfgMu.Lock()
	users := s.users
	api := s.api
	instanceKey := s.sessionPrefix
	s.cfgMu.Unlock()

	autoRegister := users != nil && users.AutoRegisterEnabled(context.Background())

	if users == nil || api == nil {
		return "" // identity mapping not wired: keep legacy behaviour
	}
	u, err := api.GetUserInfo(slackUserID)
	if err != nil {
		// Slack itself failed. Not the sender's fault and not a policy
		// decision, so let the message through on the legacy path rather
		// than blocking work on a transient API error.
		return ""
	}
	if _, rerr := agentchannels.ResolveWickUser(context.Background(), users, senderIdentityFrom(slackUserFrom(u), slackUserID), autoRegister, "slack", instanceKey); rerr != nil {
		return identityErrorMessage(rerr, autoRegister)
	}
	return ""
}
