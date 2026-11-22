package channelidentity

import (
	"context"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/entity"
)

// alerts.go decides WHERE admin notices (new user waiting, user approved,
// account needs linking) are delivered, so a busy instance can send them to
// one place instead of every admin's every door.
//
// The setting is a comma-separated list of targets:
//
//	all                      every admin, browser push + every chat DM (default, also when empty)
//	off                      no admin notices at all
//	push                     browser push to every admin
//	slack                    DM every admin through any Slack bot they are linked on
//	slack:acme               DM every admin only through the instance "slack:acme"
//	slack:acme#C01TESTCHAN   post ONCE into that channel through that instance
//
// Entries combine: "push,slack:acme#C01TESTCHAN" pushes to admins and posts one
// message to the channel, with no DMs. Unknown entries are ignored, so a typo
// narrows delivery rather than widening it. Notices to the user themselves
// ("your account was approved") are not affected.

// ChannelPoster posts a message into a room (a Slack channel) through one
// channel instance. Optional: without it, room targets are skipped.
type ChannelPoster interface {
	PostToRoom(ctx context.Context, channelType, instanceKey, roomID, text string) error
}

// AlertRoom is one room target.
type AlertRoom struct {
	ChannelType string
	InstanceKey string
	RoomID      string
}

// AlertTargets is the parsed setting.
type AlertTargets struct {
	All   bool
	Off   bool
	Push  bool
	Doors []string // channel types or instance keys an admin DM may go through
	Rooms []AlertRoom
}

// ParseAlertTargets reads the setting. Empty means All.
func ParseAlertTargets(raw string) AlertTargets {
	var t AlertTargets
	seen := false
	for _, e := range strings.Split(raw, ",") {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		seen = true
		switch strings.ToLower(e) {
		case "all":
			t.All = true
			continue
		case "off", "none":
			t.Off = true
			continue
		case "push":
			t.Push = true
			continue
		}
		if inst, room, ok := strings.Cut(e, "#"); ok {
			inst, room = strings.TrimSpace(inst), strings.TrimSpace(room)
			if inst == "" || room == "" {
				continue
			}
			ct, _, _ := strings.Cut(inst, ":")
			t.Rooms = append(t.Rooms, AlertRoom{ChannelType: ct, InstanceKey: inst, RoomID: room})
			continue
		}
		t.Doors = append(t.Doors, e)
	}
	if !seen {
		t.All = true
	}
	if t.Off {
		return AlertTargets{Off: true}
	}
	return t
}

// allowsDoor reports whether an admin DM may go out on a connection.
func (t AlertTargets) allowsDoor(channelType, instanceKey string) bool {
	if t.All {
		return true
	}
	for _, d := range t.Doors {
		if d == channelType || d == instanceKey {
			return true
		}
	}
	return false
}

func (n *Notifier) alertTargets() AlertTargets {
	if n.AlertTargets == nil {
		return AlertTargets{All: true}
	}
	return ParseAlertTargets(n.AlertTargets())
}

// notifyAdmins delivers one admin notice according to the alert targets.
// skipID is a user who must not get the notice (the subject of it).
func (n *Notifier) notifyAdmins(ctx context.Context, skipID, title, body, url, what string) {
	t := n.alertTargets()
	if t.Off {
		return
	}
	text := joinNotice(title, body, url)
	for _, r := range t.Rooms {
		if n.Rooms == nil {
			log.Warn().Str("target", r.InstanceKey+"#"+r.RoomID).
				Msg("channel identity: alert room set but this build cannot post to rooms")
			continue
		}
		if err := n.Rooms.PostToRoom(ctx, r.ChannelType, r.InstanceKey, r.RoomID, text); err != nil {
			log.Warn().Err(err).Str("target", r.InstanceKey+"#"+r.RoomID).
				Msg("channel identity: alert room delivery failed")
		}
	}
	if !t.All && !t.Push && len(t.Doors) == 0 {
		return // rooms only
	}
	if n.Admins == nil {
		return
	}
	admins, err := n.Admins.ListApprovedAdmins(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("channel identity: cannot list admins for " + what)
		return
	}
	if len(admins) == 0 && len(t.Rooms) == 0 {
		// Worth a log line: nobody will ever see the notice otherwise, and
		// silence here looks identical to "notification delivered".
		log.Warn().Msg("channel identity: no approved admin to notify for " + what)
		return
	}
	for _, a := range admins {
		if a == nil || a.ID == skipID {
			continue
		}
		n.deliverTo(ctx, a.ID, title, body, url, t)
	}
}

func joinNotice(title, body, url string) string {
	text := title + "\n" + body
	if url != "" {
		text += "\n" + url
	}
	return text
}

// AlertConfigOwner and AlertConfigTargets name the config row holding the
// setting (configs owned rows: owner + key).
const (
	AlertConfigOwner   = "user_alerts"
	AlertConfigTargets = "targets"
)

// AlertConfigRows is the row seeded at boot. Empty value = all.
func AlertConfigRows() []entity.Config {
	return []entity.Config{{
		Key:  AlertConfigTargets,
		Type: "text",
		Description: "Where admin notices about accounts (new user waiting, user approved, account needs linking) go. " +
			"Comma list: all (default) · off · push · slack (DM via any Slack bot) · <instance key> (DM via that bot only) · " +
			"<instance key>#<channel id> (post once into that channel). Example: push,slack:acme#C01TESTCHAN.",
	}}
}
