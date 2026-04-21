// Package converttext is a stateless text-conversion tool. Wick mounts
// it under /tools/{meta.Key} once per app.RegisterTool call — the same
// Register function can back multiple cards, each with its own meta
// and seeded Config.
//
// Runtime-editable knobs live in Config (see config.go); live values
// are read via c.Cfg("init_text") / c.Cfg("init_type").
package converttext

import (
	"github.com/yogasw/wick/pkg/tool"
)

// Register wires the convert-text routes on the scoped Router. All
// paths are relative to the instance's /tools/{meta.Key} base.
func Register(r tool.Router) {
	r.GET("/", index)
	r.POST("/", convert)
	r.Static("/static/", StaticFS)
}

func index(c *tool.Ctx) {
	seed := c.Cfg("init_text")
	tp := ConvertType(c.Cfg("init_type"))
	c.HTML(IndexBody(c.Base(), seed, tp, Convert(seed, tp)))
}

func convert(c *tool.Ctx) {
	text := c.Form("text")
	ct := ConvertType(c.Form("type"))
	c.HTML(IndexBody(c.Base(), text, ct, Convert(text, ct)))
}
