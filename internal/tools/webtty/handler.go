// Package webtty mounts a browser-based terminal under /tools/webtty.
// It wraps github.com/yogasw/web-tty, which spawns gotty internally
// and proxies HTTP + WebSocket traffic — no extra port is exposed
// outside the host.
//
// The tool is admin-only (System tag) and can be toggled on/off via
// the Enabled config flag without a redeploy.
package webtty

import (
	"net/http"

	webttylib "github.com/yogasw/web-tty"
	"github.com/yogasw/wick/pkg/tool"
)

// Register wires webtty routes on the scoped Router.
func Register(r tool.Router) {
	base := r.Meta().Path // /tools/webtty
	ttyAbsMount := base + "/tty"

	srv := webttylib.New(webttylib.Config{
		Prefix: ttyAbsMount,
	})

	r.GET("/", index)
	r.Static("/static/", StaticFS)

	// web-tty's Handler() already strips ttyAbsMount internally (via
	// http.StripPrefix), so no outer StripPrefix is needed here.
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
