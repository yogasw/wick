// Package webtty mounts a browser-based terminal under /tools/webtty.
// It proxies HTTP + WebSocket to an embedded gotty process — no extra
// port is exposed outside the host.
//
// The tool is admin-only (System tag) and can be toggled on/off via
// the Enabled config flag without a redeploy.
package webtty

import (
	"net/http"

	"github.com/yogasw/wick/internal/tty"
	"github.com/yogasw/wick/pkg/tool"
)

// Register wires webtty routes on the scoped Router.
func Register(r tool.Router) {
	base := r.Meta().Path // /tools/webtty
	ttyAbsMount := base + "/tty"

	srv := tty.New(tty.Config{
		Prefix: ttyAbsMount,
	})

	r.GET("/", index)
	r.Static("/static/", StaticFS)

	r.HandleRaw("/tty/", func(cfg tool.ConfigReader) http.Handler {
		inner := srv.Handler()
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if cfg.GetOwned("webtty", "enabled") != "true" {
				http.Error(w, "Terminal is disabled.", http.StatusForbidden)
				return
			}
			inner.ServeHTTP(w, req)
		})
	})
}

func index(c *tool.Ctx) {
	c.HTML(IndexBody(c.Base(), c.CfgBool("enabled")))
}
