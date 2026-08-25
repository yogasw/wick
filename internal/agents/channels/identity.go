package channels

import (
	"context"
	"errors"
	"strings"
)

// identity.go maps an inbound channel sender to the wick user whose identity
// the agent should act with — the part that is the same for every transport.
//
// It matters because the session owner decides the MCP credential the spawned
// agent carries: get it wrong and the turn runs with someone else's connector
// access, or with the synthetic admin. A platform user id is not a wick
// identity, so the join has to happen on something both sides agree on.
//
// Each channel supplies its own SenderIdentity and its own reply wording; the
// resolution order, the auto-register rule, and the approval gate live here so
// the two cannot drift apart. A second transport quietly skipping the approval
// check would be a way around the dashboard's own gate.

// ErrEmailRequired is returned when the channel gave nothing to match a wick
// account on — no email, and no way to synthesise a stable stand-in.
var ErrEmailRequired = errors.New("email is required")

// ErrNoAccount is returned when the sender's email resolved but no wick user
// carries it and auto-registration is off.
var ErrNoAccount = errors.New("no wick account for this email")

// ErrToolAccessDenied is returned when the sender maps to a real wick user who
// is not permitted to use the agents tool — the tool disabled, or missing a
// required filter tag.
var ErrToolAccessDenied = errors.New("no access to the agents tool")

// ErrPendingApproval is returned when the sender maps to a real wick account
// that no admin has approved yet. Split from ErrToolAccessDenied because the
// fix differs: this one waits on an approval, the other on a grant.
var ErrPendingApproval = errors.New("account is pending admin approval")

// ErrGuestNotAllowed is returned for a restricted/guest account on channels
// that expose the distinction.
var ErrGuestNotAllowed = errors.New("guest accounts may not use this channel")

// SenderIdentity is what a channel knows about an inbound sender, reduced to
// the fields identity resolution needs.
//
// Email is the join key. A channel that reports a real address (Slack) passes
// it through; one that does not (Telegram) synthesises a stable stand-in with
// SyntheticEmail. Either way it must identify exactly one person and must not
// collide with a real address — see SyntheticEmail.
type SenderIdentity struct {
	// ExternalUserID is the platform's own id for this person (Slack U…,
	// Telegram numeric id). Stable across name and email changes, which is why
	// the channel-identity link is keyed on it.
	ExternalUserID string
	Email          string
	Name           string
	IsBot          bool
	IsGuest        bool
	IsDeleted      bool
}

// syntheticEmailDomain is a reserved TLD (RFC 6761): it can never be
// registered, resolved, or received at.
//
// That is the entire point. A synthetic address has to be unmistakably not an
// inbox, because it is matched against real accounts with FindByEmail. Using a
// domain someone can actually own — telegram.org, say — would mean a person
// who registers a wick account under it gets matched into somebody else's
// channel session.
const syntheticEmailDomain = "telegram.local"

// SyntheticEmail builds the stand-in address for a channel that reports no
// email, e.g. "8812@telegram.local".
//
// It is a lookup key, not a contact address: nothing is ever delivered to it.
// An admin merging the placeholder account into the person's real one is the
// intended path, and it works because the channel-identity link is keyed on
// the platform id rather than on this string.
//
// Returns "" for an empty id, so a caller cannot build "@telegram.local" and
// have every unidentified sender collapse onto one account.
func SyntheticEmail(externalUserID string) string {
	id := strings.TrimSpace(externalUserID)
	if id == "" {
		return ""
	}
	return strings.ToLower(id) + "@" + syntheticEmailDomain
}

// UserResolver looks a wick user up, creates one when allowed, and answers
// whether that user may use the agents tool at all. Implemented by the server
// over login.Service.
type UserResolver interface {
	// FindByChannelIdentity resolves an EXISTING link from the channel account
	// itself. Consulted before FindByEmail because the account id is stable
	// while the email is not: when someone's channel email changes, an
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
	// Asked of the host, not read from a channel's own config: channel rows
	// are per-owner, so a per-channel switch would let any user who adds their
	// own bot mint pending wick accounts. The decision belongs to whoever
	// administers the install.
	AutoRegisterEnabled(ctx context.Context) bool
	// RecordIdentity remembers this channel account belongs to that wick user,
	// so a later notification has a destination. Called on every resolved
	// message, not only the first, so it must be an upsert on the host side.
	RecordIdentity(ctx context.Context, wickUserID, externalUserID, displayName, email string)
	// CanUseAgents reports whether the user passes the same tool-access gate
	// the dashboard applies to /tools/agents. A channel is a second door onto
	// the same tool, so it has to ask the same question — otherwise someone
	// blocked in the web UI could simply message the bot instead.
	CanUseAgents(ctx context.Context, wickUserID string) bool
}

// ResolveWickUser maps a channel sender to a wick user id.
//
// autoRegister creates a wick account for an unrecognised email. The account
// is created UNAPPROVED: a channel reporting an address does not prove the
// sender owns it, and workspace membership is not the same claim as a wick
// registration. An admin approving the row is what turns one into the other.
//
// A channel sender is never promoted to admin, even when the address appears
// in the admin list — that path has to prove ownership of the address.
//
// channelType is the transport ("slack", "telegram"); instanceKey namespaces
// the link so two bots in different workspaces cannot collide on the same
// platform id.
func ResolveWickUser(
	ctx context.Context,
	users UserResolver,
	u SenderIdentity,
	autoRegister bool,
	channelType, instanceKey string,
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
	id, ok := users.FindByChannelIdentity(ctx, channelType, instanceKey, u.ExternalUserID)
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
		newID, rerr := users.RegisterFromChannel(ctx, email, u.Name, channelType)
		if rerr != nil {
			return "", rerr
		}
		id = newID
	}
	// Same gate the dashboard applies. Asked AFTER resolution so the identity
	// is recorded either way, and asked BEFORE the caller spawns anything.
	// Approval is reported separately from a missing grant: both end in a
	// refusal, but one waits on an admin clicking Approve and the other on
	// being given access. A sender told the wrong one chases the wrong fix.
	if !users.IsApproved(ctx, id) {
		return "", ErrPendingApproval
	}
	if !users.CanUseAgents(ctx, id) {
		return "", ErrToolAccessDenied
	}
	return id, nil
}
