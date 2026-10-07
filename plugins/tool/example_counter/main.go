// Command example_counter is a small tool plugin: a page with a form that
// counts characters, words, and lines, the same count as JSON, and one
// webhook that counts a posted text. It shows the three surfaces a tool
// plugin has — a wrapped page, a JSON endpoint, and a session-less webhook.
package main

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/a-h/templ"

	"github.com/yogasw/wick/pkg/entity"
	"github.com/yogasw/wick/pkg/plugin/toolplugin"
	"github.com/yogasw/wick/pkg/tool"
)

type Config struct {
	WebhookSecret string `wick:"desc=Shared secret the webhook caller sends in X-Counter-Secret. Empty disables the webhook.;secret"`
}

// Counts is the result of one count.
type Counts struct {
	Chars int `json:"chars"`
	Words int `json:"words"`
	Lines int `json:"lines"`
}

func count(s string) Counts {
	c := Counts{Chars: utf8.RuneCountInString(s), Words: len(strings.Fields(s))}
	if s != "" {
		c.Lines = strings.Count(s, "\n") + 1
	}
	return c
}

func module() tool.Module {
	return tool.Module{
		Meta: tool.Tool{
			Key:         "example_counter",
			Name:        "Counter (plugin)",
			Description: "Count characters, words, and lines — served by a tool plugin.",
			Icon:        "#",
			Category:    "Text",
		},
		Configs:  entity.StructToConfigs(Config{}),
		Register: register,
	}
}

func register(r tool.Router) {
	r.GET("/", func(c *tool.Ctx) { c.HTML(page(c, "", nil)) })
	r.POST("/", func(c *tool.Ctx) {
		text := c.Form("text")
		res := count(text)
		if c.WantsJSON() {
			c.JSON(http.StatusOK, res)
			return
		}
		c.HTML(page(c, text, &res))
	})
	r.GET("/api/count", func(c *tool.Ctx) { c.JSON(http.StatusOK, count(c.Query("text"))) })

	wh := r.WebhookGroup("/webhook")
	wh.POST("/count", func(c *tool.WebhookCtx) {
		secret := c.Cfg("webhook_secret")
		if secret == "" || c.Header("X-Counter-Secret") != secret {
			c.JSON(http.StatusUnauthorized, map[string]string{"error": "bad secret"})
			return
		}
		body, err := c.Body()
		if err != nil {
			c.JSON(http.StatusRequestEntityTooLarge, map[string]string{"error": err.Error()})
			return
		}
		c.JSON(http.StatusOK, count(string(body)))
	})
}

func page(c *tool.Ctx, text string, res *Counts) templ.Component {
	return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
		u, _ := c.User()
		var b strings.Builder
		b.WriteString(`<div class="mx-auto max-w-container px-4 py-6">`)
		fmt.Fprintf(&b, `<p class="mb-3 text-sm text-black-700 dark:text-black-600">Hi %s — this page is rendered by the <code>example_counter</code> plugin process and wrapped in wick's layout.</p>`, html.EscapeString(u.Name))
		fmt.Fprintf(&b, `<form method="post" action="%s" class="flex flex-col gap-3">`, c.Base())
		fmt.Fprintf(&b, `<textarea name="text" rows="6" class="w-full rounded-lg border border-white-300 dark:border-navy-600 bg-white-100 dark:bg-navy-800 p-3 text-sm text-black-900 dark:text-white-100">%s</textarea>`, html.EscapeString(text))
		b.WriteString(`<div><button type="submit" class="rounded-lg bg-green-500 px-4 py-2 text-sm font-semibold text-white-100 hover:bg-green-600">Count</button></div></form>`)
		if res != nil {
			fmt.Fprintf(&b, `<div class="mt-4 grid grid-cols-3 gap-3">`)
			for _, kv := range []struct {
				k string
				v int
			}{{"Characters", res.Chars}, {"Words", res.Words}, {"Lines", res.Lines}} {
				fmt.Fprintf(&b, `<div class="rounded-xl border border-white-300 dark:border-navy-600 bg-white-200 dark:bg-navy-800 p-4"><p class="text-xs text-black-700 dark:text-black-600">%s</p><p class="text-2xl font-semibold text-black-900 dark:text-white-100">%d</p></div>`, kv.k, kv.v)
			}
			b.WriteString(`</div>`)
		}
		fmt.Fprintf(&b, `<p class="mt-4 text-xs text-black-700 dark:text-black-600">JSON: <code>GET %s/api/count?text=…</code> · Webhook: <code>POST %s/webhook/count</code></p></div>`, c.Base(), c.Base())
		_, err := io.WriteString(w, b.String())
		return err
	})
}

func main() {
	toolplugin.ServeTool(module())
}
