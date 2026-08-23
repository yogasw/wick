package channelidentity

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
)

// notify.go delivers account-lifecycle notices to people, across whatever doors
// they have: browser push plus every un-paused chat connection.

// PushSender delivers a browser push notification to a user's devices.
// Satisfied by pwa.PushService.
type PushSender interface {
	SendToUser(ctx context.Context, userID, title, body, url string) (int, error)
}

// ChannelSender delivers a direct message on one chat channel instance.
//
// instanceKey selects WHICH bot sends it: with several Slack instances wired,
// the message has to go out through the same one the identity belongs to, or it
// lands in a workspace where that user id means nothing.
type ChannelSender interface {
	SendDirect(ctx context.Context, channelType, instanceKey, externalUserID, text string) error
}

// AdminLister returns the approved admins to notify.
type AdminLister interface {
	ListApprovedAdmins(ctx context.Context) ([]*entity.User, error)
}

// Notifier fans an account event out to every reachable destination.
//
// Every send is best-effort. These notices are announcements about an action
// that already happened — an approval must not be rolled back because a Slack
// DM failed, and a registration must not be refused because no admin could be
// reached.
type Notifier struct {
	Store    *Store
	Push     PushSender
	Channels ChannelSender
	Admins   AdminLister
	// AppURL is the base URL used to deep-link a notification at the page where
	// the reader can act. Empty = no link.
	AppURL func() string
}

// NotifyAdminsNewUser tells approved admins that someone registered through a
// channel and is waiting for approval.
//
// Only APPROVED admins are notified. The notice names a pending registration,
// so sending it to an unapproved account would leak that to someone who has
// not themselves been vetted.
func (n *Notifier) NotifyAdminsNewUser(ctx context.Context, newUser *entity.User, source string) {
	if n == nil || n.Admins == nil || newUser == nil {
		return
	}
	admins, err := n.Admins.ListApprovedAdmins(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("channel identity: cannot list admins to notify")
		return
	}
	if len(admins) == 0 {
		// Worth a log line: nobody will ever see the request otherwise, and
		// silence here looks identical to "notification delivered".
		log.Warn().Str("email", newUser.Email).
			Msg("channel identity: new user registered but no approved admin to notify")
		return
	}

	title := "New user awaiting approval"
	body := fmt.Sprintf("%s (%s) registered via %s and needs approval.",
		displayName(newUser), newUser.Email, source)
	url := n.link("/admin/users")

	for _, a := range admins {
		if a == nil || a.ID == newUser.ID {
			continue // don't ask someone to approve themselves
		}
		n.deliver(ctx, a.ID, title, body, url)
	}
}

// NotifyUserApproved tells a user their account was approved, on every door
// they have — including the channel they registered from, which is where they
// are most likely to be looking.
//
// Admins get a copy. Approval is the moment an account gains access, so the
// other admins seeing it is the audit trail: without it, only the admin who
// clicked knows, and nobody can notice an approval they did not expect.
func (n *Notifier) NotifyUserApproved(ctx context.Context, user *entity.User) {
	if n == nil || user == nil {
		return
	}
	n.deliver(ctx, user.ID,
		"Account approved",
		"Your wick account has been approved. You can start a session now.",
		n.link("/"))

	if n.Admins == nil {
		return
	}
	admins, err := n.Admins.ListApprovedAdmins(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("channel identity: cannot list admins for approval notice")
		return
	}
	title := "User approved"
	body := fmt.Sprintf("%s (%s) is now approved and can use wick.",
		displayName(user), user.Email)
	url := n.link("/admin/users")
	for _, a := range admins {
		if a == nil || a.ID == user.ID {
			continue // they already got the user-facing notice above
		}
		n.deliver(ctx, a.ID, title, body, url)
	}
}

// NotifyAdminsMergeNeeded tells admins a channel account exists that cannot be
// matched to anyone, so it needs merging by hand.
//
// Distinct from NotifyAdminsNewUser because the action differs: that one asks
// for an approval, this one asks "who is this?". Approving it as its own
// account is almost never right — it would leave the person with two.
func (n *Notifier) NotifyAdminsMergeNeeded(ctx context.Context, userID, displayName, source string) {
	if n == nil || n.Admins == nil {
		return
	}
	admins, err := n.Admins.ListApprovedAdmins(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("channel identity: cannot list admins for merge notice")
		return
	}
	title := "Channel account needs linking"
	body := fmt.Sprintf("%q messaged wick via %s. %s reports no email, so wick cannot tell "+
		"which account this is — link it to the right user under Admin → Users.",
		displayName, source, source)
	url := n.link("/admin/users")
	for _, a := range admins {
		if a == nil || a.ID == userID {
			continue
		}
		n.deliver(ctx, a.ID, title, body, url)
	}
}

// deliver pushes to the user's browsers and DMs every un-paused connection.
// Each destination is independent: one failing must not stop the others.
func (n *Notifier) deliver(ctx context.Context, userID, title, body, url string) {
	if n.Push != nil {
		if _, err := n.Push.SendToUser(ctx, userID, title, body, url); err != nil {
			log.Debug().Err(err).Str("user_id", userID).
				Msg("channel identity: push delivery failed")
		}
	}
	if n.Channels == nil || n.Store == nil {
		return
	}
	// ActiveForUser excludes paused rows, so a pause is enforced HERE rather
	// than only greyed out in the UI.
	conns, err := n.Store.ActiveForUser(ctx, userID)
	if err != nil {
		log.Warn().Err(err).Str("user_id", userID).
			Msg("channel identity: cannot list connections for delivery")
		return
	}
	text := title + "\n" + body
	if url != "" {
		text += "\n" + url
	}
	for _, c := range conns {
		if err := n.Channels.SendDirect(ctx, c.ChannelType, c.InstanceKey, c.ExternalUserID, text); err != nil {
			log.Debug().Err(err).
				Str("user_id", userID).
				Str("channel", c.ChannelType).
				Str("instance", c.InstanceKey).
				Msg("channel identity: channel delivery failed")
		}
	}
}

// link joins AppURL with a path. Returns "" when no app URL is configured, so
// a notification without a usable base URL carries no half-formed link.
func (n *Notifier) link(path string) string {
	if n.AppURL == nil {
		return ""
	}
	base := n.AppURL()
	if base == "" {
		return ""
	}
	if len(base) > 0 && base[len(base)-1] == '/' {
		base = base[:len(base)-1]
	}
	return base + path
}

func displayName(u *entity.User) string {
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}
