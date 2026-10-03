// Package notifications exposes Wick's in-process notification service as a
// fixed connector.
package notifications

import (
	"crypto/hmac"
	"errors"
	"strings"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/pkg/pwa"
	"github.com/yogasw/wick/internal/tags"
	"github.com/yogasw/wick/pkg/connector"
	"github.com/yogasw/wick/pkg/tool"
	"github.com/yogasw/wick/pkg/wickdocs"
	"gorm.io/gorm"
)

const Key = "notifications"

type Configs struct{}

type Deps struct {
	DB   *gorm.DB
	Push *pwa.PushService
}

func Meta() connector.Meta {
	return connector.Meta{
		Key:  Key,
		Name: "Notifications",
		Description: "Send Wick notifications to subscribed browsers and installed web apps. " +
			"Send to YOURSELF with no addressing at all, or to somebody else by the opaque PN ID shown on their Account page.",
		Icon:  "🔔",
		Fixed: true,
	}
}

func Module(deps Deps) connector.Module {
	m := Meta()
	m.DefaultTags = []tool.DefaultTag{tags.Connector, tags.Platform}
	return connector.Module{
		Meta:       m,
		Operations: Operations(deps),
	}
}

// sendMeInput addresses nobody: the recipient IS the caller, which wick
// already knows from the credential the call arrived on. That is the whole
// point — this op exists because asking a person to fetch their own PN ID and
// paste it back, so wick can send them a notification wick could already
// address, was a detour around information it had all along.
type sendMeInput struct {
	Title string `wick:"desc=Notification title shown by the browser. Defaults to Wick notification."`
	Body  string `wick:"textarea;desc=Notification body text. Keep it short, browsers may truncate long text."`
	// Where the click lands. A notification that says "done" and opens a
	// blank app makes you hunt for what it was about; wick knows which
	// conversation asked, so it can just take you back to it.
	Link string `wick:"dropdown=conversation|home|custom;desc=Where clicking the notification goes. conversation (default) opens the chat this call came from. home opens wick's front page. custom uses the url field."`
	URL  string `wick:"desc=Relative app URL to open when link=custom, for example /tools/agents. Ignored for the other link modes."`
}

type sendInput struct {
	PushID string `wick:"required;desc=Recipient PN ID. Ask the user to open Account → Notifications and copy the PN ID shown there, the ID starts with pn_ and does not expose the user's real user ID."`
	Title  string `wick:"desc=Notification title shown by the browser. Defaults to Wick notification."`
	Body   string `wick:"textarea;desc=Notification body text. Keep it short, browsers may truncate long text."`
	URL    string `wick:"desc=Relative app URL to open when the notification is clicked, for example /tools/agents. Defaults to /."`
}

func Operations(deps Deps) []connector.Category {
	h := handlers{deps: deps}
	return []connector.Category{
		connector.Cat("Notifications", "Send a browser push notification to yourself, or to a PN ID.",
			connector.Op("send_to_me", "Send Notification To Me",
				"Notify the person this call is running for — no id, no lookup, no asking them for anything. "+
					"Use it to say a long job finished: the recipient comes from the credential, so there is nothing to address. "+
					"By default clicking the notification opens the conversation the call came from (link=conversation), "+
					"because a notification that does not take you back to what it was about makes you hunt for it; "+
					"pass link=home for wick's front page or link=custom with url for anywhere else. "+
					"Returns {ok, sent, link}, where sent counts the browsers/devices reached — sent=0 means the account has no "+
					"subscribed device yet (Account → Notifications), not that the send failed. "+
					"To notify SOMEBODY ELSE use send_to_push_id, which needs their PN ID and admin rights.",
				sendMeInput{}, h.sendToMe, wickdocs.Docs{}),
			connector.Op("send_to_push_id", "Send Notification To PN ID",
				"Send a notification to every active subscribed browser/device for one opaque PN ID — that is, to SOMEBODY ELSE; "+
					"for yourself use send_to_me, which needs no id at all. "+
					"Payload: push_id is required and comes from Account → Notifications; title/body/url control the browser notification content and click target. "+
					"Returns {ok, sent}. This connector does not expose user search, user listing, or device inspection.",
				sendInput{}, h.sendToUser, wickdocs.Docs{}),
		),
	}
}

type handlers struct {
	deps Deps
}

func (h handlers) requireAdmin(c *connector.Ctx) (*entity.User, error) {
	u := login.GetUser(c.Context())
	if u == nil {
		return nil, errors.New("not authenticated")
	}
	if !u.IsAdmin() {
		return nil, errors.New("access denied")
	}
	return u, nil
}

// sessionURL is the app path for one conversation — the same one the bell in
// the composer sends people to when a session goes idle, so a notification
// raised by an agent and one raised by wick itself land in the same place.
func sessionURL(sessionID string) string {
	return "/tools/agents/sessions/" + sessionID
}

// clickURL resolves where the notification goes, and says which mode it ended
// up in. A pure function so the interesting half of this op is testable
// without a database, a push service or a browser.
//
// "conversation" is the default and silently degrades to home when there is no
// calling session (a REST call, a scheduled job): the alternative is refusing
// to send a notification over its click target, which is the wrong thing to
// fail on.
func clickURL(link, custom, sessionID string) (url, mode string) {
	switch strings.TrimSpace(strings.ToLower(link)) {
	case "home":
		return "/", "home"
	case "custom":
		if u := strings.TrimSpace(custom); u != "" {
			return u, "custom"
		}
		return "/", "home"
	default: // "" and "conversation"
		if sessionID != "" {
			return sessionURL(sessionID), "conversation"
		}
		// A custom url is a better guess than the front page when the caller
		// gave one and there is no conversation to open.
		if u := strings.TrimSpace(custom); u != "" {
			return u, "custom"
		}
		return "/", "home (no calling conversation)"
	}
}

// sendToMe notifies the caller. No admin gate and no PN ID: the only reachable
// recipient is the person already on the other end of this call, so there is
// nothing here to abuse — no user table is read, no id is resolved, and a
// caller cannot address anybody but themselves.
func (h handlers) sendToMe(c *connector.Ctx) (any, error) {
	uid := strings.TrimSpace(c.CallerUserID())
	if uid == "" {
		// Not an error in the sense of something being broken: some calls
		// genuinely have no person behind them.
		return nil, errors.New("this call has no user behind it (a system or scheduled run), so there is no \"me\" to notify — " +
			"use send_to_push_id with the recipient's PN ID instead")
	}
	url, mode := clickURL(c.Input("link"), c.Input("url"), c.SessionID())
	sent, err := h.deps.Push.SendToUser(c.Context(), uid, c.Input("title"), c.Input("body"), url)
	if err != nil {
		return nil, err
	}
	res := map[string]any{
		"ok":   sent > 0,
		"sent": sent,
		"link": mode,
		"url":  url,
	}
	if name := c.UserName(uid); name != "" {
		res["recipient"] = name
	}
	if sent == 0 {
		// Nothing failed — there was simply nowhere to deliver. Said plainly,
		// because "ok: false" on its own reads as an error and sends people
		// looking for one.
		res["note"] = "no subscribed browser or device on this account yet — open Account → Notifications and enable them"
	}
	return res, nil
}

func (h handlers) sendToUser(c *connector.Ctx) (any, error) {
	if _, err := h.requireAdmin(c); err != nil {
		return nil, err
	}
	u, err := h.resolveUser(c)
	if err != nil {
		return nil, err
	}
	sent, err := h.deps.Push.SendToUser(c.Context(), u.ID, c.Input("title"), c.Input("body"), c.Input("url"))
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":   sent > 0,
		"sent": sent,
	}, nil
}

func (h handlers) resolveUser(c *connector.Ctx) (*entity.User, error) {
	return h.resolvePushID(c, c.Input("push_id"))
}

func (h handlers) resolvePushID(c *connector.Ctx, pushID string) (*entity.User, error) {
	pushID = strings.TrimSpace(pushID)
	if pushID == "" {
		return nil, errors.New("push_id is required")
	}
	var users []entity.User
	if err := h.deps.DB.WithContext(c.Context()).Find(&users).Error; err != nil {
		return nil, err
	}
	for i := range users {
		if hmac.Equal([]byte(h.deps.Push.UserPushID(users[i].ID)), []byte(pushID)) {
			return &users[i], nil
		}
	}
	return nil, errors.New("user not found")
}
