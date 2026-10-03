package main

import (
	"context"
	"html"
	"io"
	"net/http"

	"github.com/a-h/templ"

	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// Config is the tool's runtime-editable config, edited on the tool's
// settings page in wick and pushed to the plugin whenever it changes.
type Config struct {
	Greeting string `wick:"desc=Text shown at the top of the page.;required"`
}

// Module is the whole tool: meta, config, and routes.
func Module() tool.Module {
	return tool.Module{
		Meta: tool.Tool{
			Key:         "template_tool", // lowercase, digits, '_' only
			Name:        "Template Tool",
			Description: "Starter tool plugin.",
			Icon:        "🧩",
			Category:    "Plugins",
		},
		Configs:  entity.StructToConfigs(Config{}),
		Register: register,
	}
}

func register(r tool.Router) {
	// A page: c.HTML renders a fragment; wick wraps it in its layout.
	r.GET("/", func(c *tool.Ctx) {
		u, _ := c.User()
		c.HTML(raw("<h2>" + html.EscapeString(c.Cfg("greeting")) + "</h2><p>Signed in as " + html.EscapeString(u.Email) + "</p>"))
	})
	// JSON: passes through unwrapped.
	r.GET("/api/ping", func(c *tool.Ctx) { c.JSON(http.StatusOK, map[string]string{"pong": c.Meta().Key}) })
	// Webhooks: only routes declared here are reachable without a login,
	// so verify a signature or shared secret before trusting the payload.
	wh := r.WebhookGroup("/webhook")
	wh.POST("/event", func(c *tool.WebhookCtx) { c.JSON(http.StatusOK, map[string]bool{"ok": true}) })
}

// raw writes trusted HTML. Real tools usually use templ components instead.
func raw(s string) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		_, err := io.WriteString(w, s)
		return err
	})
}
