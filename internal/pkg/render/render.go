// Package render builds the tool.RenderFunc that wraps a body fragment
// in wick's page shell (Layout + Navbar). Lives outside internal/pkg/ui
// to avoid an import cycle with internal/login (which already depends
// on ui).
package render

import (
	"context"
	"io"
	"net/http"

	"github.com/a-h/templ"

	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/pkg/ui"
	"github.com/yogasw/wick/pkg/tool"
)

// NewToolRenderer builds a tool.RenderFunc that wraps a body fragment
// in the wick page shell (Layout + Navbar + per-tool chrome) and
// writes the full HTML response. meta.Name is the <title>; meta.Key
// drives the floating [⚙ Settings] pill, shown to admins when
// hasConfigs is true.
//
// Setup-required banners are rendered per-module on the manager
// detail pages (/manager/tools/{key}, /manager/jobs/{key}) — the tool
// renderer no longer surfaces them because operators see the warning
// on the module's own surface when it needs configuration.
//
// Handlers that return JSON, file downloads, or redirects should
// write to the ResponseWriter directly and not call the returned
// RenderFunc.
func NewToolRenderer(meta tool.Tool, hasConfigs bool) tool.RenderFunc {
	return func(w http.ResponseWriter, r *http.Request, body templ.Component) {
		user := login.GetUser(r.Context())
		isAdmin := user != nil && user.IsAdmin()
		inner := templ.ComponentFunc(func(ctx context.Context, out io.Writer) error {
			if err := ui.Navbar(user).Render(ctx, out); err != nil {
				return err
			}
			if err := ui.ToolChrome(meta.Key, hasConfigs, isAdmin).Render(ctx, out); err != nil {
				return err
			}
			return body.Render(ctx, out)
		})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		ctx := templ.WithChildren(r.Context(), inner)
		_ = ui.Layout(meta.Name).Render(ctx, w)
	}
}
