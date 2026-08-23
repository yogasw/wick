package slack

import (
	"context"
	"errors"
	"strings"

	slackgo "github.com/slack-go/slack"
)

// identity.go maps an inbound Slack sender to the wick user whose identity
// the agent should act with.
//
// This matters because the session owner decides the MCP credential the
// spawned agent carries: get it wrong and the turn runs with someone else's
// connector access, or with the synthetic admin. Slack's user ID is not a
// wick identity, so the join has to happen on something both sides agree on
// — the email address.

// ErrEmailRequired is returned when Slack gave no email for the sender, so
// there is nothing to match a wick account on.
//
// Slack omits the email in ordinary, non-guest cases: the app's token is
// missing the users:read.email scope (users.info then returns a blank email
// with NO error), or the sender is a bot / app rather than a person. Both are
// reported as this error so the operator sees a cause rather than a session
// that silently runs as the wrong principal.
var ErrEmailRequired = errors.New("email is required")

// ErrNoAccount is returned when the sender's email resolved but no wick user
// carries it and auto-registration is off.
var ErrNoAccount = errors.New("no wick account for this email")

// ErrToolAccessDenied is returned when the sender maps to a real wick user who
// is not permitted to use the agents tool — pending approval, the tool
// disabled, or missing a required filter tag.
var ErrToolAccessDenied = errors.New("no access to the agents tool")

// ErrPendingApproval is returned when the sender maps to a real wick account
// that no admin has approved yet. Split from ErrToolAccessDenied because the
// fix differs: this one waits on an approval, the other on a grant.
var ErrPendingApproval = errors.New("account is pending admin approval")

// ErrGuestNotAllowed is returned for a Slack guest account (single- or
// multi-channel guest, which includes Slack Connect users from another
// workspace).
//
// Guests are refused even where the workspace has none today: guest access is
// a workspace-level setting an admin can turn on later without touching wick,
// and at that moment an outside party could DM the bot. One flag check keeps
// that from becoming an account.
var ErrGuestNotAllowed = errors.New("guest accounts may not use this channel")

// SlackUser is the subset of users.info this package needs.
type SlackUser struct {
	Email     string
	Name      string
	IsBot     bool
	IsGuest   bool
	IsDeleted bool
}

// UserResolver looks a wick user up by email, creates one when allowed, and
// answers whether that user may use the agents tool at all.
// Implemented by the server over login.Service.
type UserResolver interface {
	// FindByChannelIdentity resolves an EXISTING link from the channel account
	// itself. Consulted before FindByEmail because the account id is stable
	// while the email is not: when someone's Slack email changes, an
	// email-first lookup misses and would register a SECOND wick account for a
	// person who already has one.
	FindByChannelIdentity(ctx context.Context, channelType, instanceKey, externalUserID string) (wickUserID string, ok bool)
	// FindByEmail returns the wick user id for an email, or ok=false. Used only
	// for a FIRST link, where no channel identity exists yet.
	FindByEmail(ctx context.Context, email string) (wickUserID string, ok bool)
	// RegisterFromChannel creates an unapproved wick user for a verified
	// channel identity and returns its id.
	RegisterFromChannel(ctx context.Context, email, name, source string) (wickUserID string, err error)
	// IsApproved reports whether an admin has approved the account. Checked
	// before CanUseAgents so a pending account is told to wait rather than told
	// it lacks a permission an admin never had the chance to grant.
	IsApproved(ctx context.Context, wickUserID string) bool
	// AutoRegisterEnabled reports whether wick may create an account for an
	// unmatched sender.
	//
	// Asked of the host, not read from this channel's own config: channel rows
	// are per-owner, so a per-channel switch would let any user who adds their
	// own bot mint pending wick accounts. The decision belongs to whoever
	// administers the install.
	AutoRegisterEnabled(ctx context.Context) bool
	// RecordIdentity remembers this channel account belongs to that wick user,
	// so a later notification has a destination. Called on every resolved
	// message, not only the first, so it must be an upsert on the host side.
	RecordIdentity(ctx context.Context, wickUserID, externalUserID, displayName, email string)
	// CanUseAgents reports whether the user passes the same tool-access gate
	// the dashboard applies to /tools/agents (approval, per-tool disable, and
	// filter tags). Slack is a second door onto the same tool, so it has to
	// ask the same question — otherwise someone blocked in the web UI could
	// simply message the bot instead.
	CanUseAgents(ctx context.Context, wickUserID string) bool
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

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// resolveWickUser maps a Slack sender to a wick user id.
//
// autoRegister creates a wick account for an unrecognised email. The account
// is created UNAPPROVED: Slack reports an email, it does not prove the sender
// owns it, and workspace membership is not the same claim as a wick
// registration. An admin approving the row is what turns one into the other.
//
// A Slack sender is never promoted to admin, even when the email appears in
// the admin list — that path has to prove ownership of the address.
func resolveWickUser(
	ctx context.Context,
	users UserResolver,
	u SlackUser,
	autoRegister bool,
	instanceKey, externalUserID string,
) (wickUserID string, err error) {
	if users == nil {
		return "", ErrEmailRequired
	}
	if u.IsBot {
		// A bot has a user id but no person behind it; there is no identity
		// to act as.
		return "", ErrEmailRequired
	}
	if u.IsGuest {
		return "", ErrGuestNotAllowed
	}
	if u.Email == "" {
		return "", ErrEmailRequired
	}
	email := strings.ToLower(u.Email)

	// Stable lookup first. An existing link survives an email change, so this
	// is what stops one person from accumulating a second account every time
	// their channel email is edited.
	id, ok := users.FindByChannelIdentity(ctx, "slack", instanceKey, externalUserID)
	if !ok {
		id, ok = users.FindByEmail(ctx, email)
	}
	if !ok {
		if !autoRegister {
			return "", ErrNoAccount
		}
		// Registering is fine on its own — it just records who this is. The
		// account still has to clear the tool gate below, which a fresh
		// unapproved row never does, so the sender is told to wait rather
		// than silently getting in.
		newID, err := users.RegisterFromChannel(ctx, email, u.Name, "slack")
		if err != nil {
			return "", err
		}
		id = newID
	}
	// Same gate the dashboard applies. Asked AFTER resolution so the identity
	// is recorded either way, and asked BEFORE the caller spawns anything.
	// Approval is reported separately from a missing grant: both end in a
	// refusal, but one waits on an admin clicking Approve and the other on being
	// given access. A sender told the wrong one chases the wrong fix.
	if !users.IsApproved(ctx, id) {
		return "", ErrPendingApproval
	}
	if !users.CanUseAgents(ctx, id) {
		return "", ErrToolAccessDenied
	}
	return id, nil
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
			id, rerr := resolveWickUser(context.Background(), users, su, autoRegister, instanceKey, slackUserID)
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
	if _, rerr := resolveWickUser(context.Background(), users, slackUserFrom(u), autoRegister, instanceKey, slackUserID); rerr != nil {
		return identityErrorMessage(rerr, autoRegister)
	}
	return ""
}
