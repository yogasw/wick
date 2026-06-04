// Package pwanotify exposes Wick's in-process PWA push notification
// service as a fixed connector.
package pwanotify

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

const Key = "pwanotify"

type Configs struct{}

type Deps struct {
	DB   *gorm.DB
	Push *pwa.PushService
}

func Meta() connector.Meta {
	return connector.Meta{
		Key:         Key,
		Name:        "PWA Notifications",
		Description: "Inspect PWA push subscription status and send Wick push notifications to subscribed users.",
		Icon:        "🔔",
		Fixed:       true,
	}
}

func Module(deps Deps) connector.Module {
	m := Meta()
	m.DefaultTags = []tool.DefaultTag{tags.Connector, tags.Communication}
	return connector.Module{
		Meta:       m,
		Operations: Operations(deps),
	}
}

type sendInput struct {
	PushID string `wick:"required;desc=Opaque PN ID shown on the user's Account page."`
	Title  string `wick:"desc=Notification title. Defaults to Wick notification."`
	Body   string `wick:"textarea;desc=Notification body."`
	URL    string `wick:"desc=Relative app URL to open on click. Default /."`
}

func Operations(deps Deps) []connector.Operation {
	h := handlers{deps: deps}
	return []connector.Operation{
		connector.OpDestructive("send_to_push_id", "Send Push To PN ID",
			"Send a PWA push notification to every active subscribed device for one opaque PN ID copied from the user's Account page. Returns {ok, sent}. This connector does not expose self-send, user search, user listing, or device inspection.",
			sendInput{}, h.sendToUser, wickdocs.Docs{}),
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
