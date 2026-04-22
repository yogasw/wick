// Package render builds the tool.RenderFunc that wraps a body fragment
// in wick's page shell (Layout + Navbar). Lives outside internal/pkg/ui
// to avoid an import cycle with internal/login (which already depends
// on ui).
package render

import (
	"context"
	"io"

	"github.com/a-h/templ"

	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/pkg/ui"
	"github.com/yogasw/wick/pkg/tool"
)

// NewToolRenderer builds a tool.RenderFunc that wraps a body fragment
// in the wick page shell (Layout + Navbar + setup banner + ToolHeader)
// and writes the full HTML response. The shared ToolHeader renders
// the tool's icon/name/description so tool bodies must omit their own
// <h1>/description block. Missing-config state is pulled from the
// *tool.Ctx via c.Missing() — no config service threading required.
//
// Handlers that return JSON, file downloads, or redirects should
// write to the ResponseWriter directly and not call the returned
// RenderFunc.
func NewToolRenderer(hasConfigs bool) tool.RenderFunc {
	return func(c *tool.Ctx, body templ.Component) {
		user := login.GetUser(c.R.Context())
		isAdmin := user != nil && user.IsAdmin()
		meta := c.Meta()
		banner := toolBanner(meta, c.Missing())
		inner := templ.ComponentFunc(func(ctx context.Context, out io.Writer) error {
			if err := ui.Navbar(user).Render(ctx, out); err != nil {
				return err
			}
			if err := ui.ScopedSetupBanner(banner, isAdmin).Render(ctx, out); err != nil {
				return err
			}
			if err := ui.ToolHeader(meta.Name, meta.Description, meta.Icon, meta.Key, hasConfigs, isAdmin).Render(ctx, out); err != nil {
				return err
			}
			return body.Render(ctx, out)
		})
		c.W.Header().Set("Content-Type", "text/html; charset=utf-8")
		ctx := templ.WithChildren(c.R.Context(), inner)
		_ = ui.Layout(meta.Name).Render(ctx, c.W)
	}
}

func toolBanner(meta tool.Tool, missing []string) *ui.MissingEntry {
	if len(missing) == 0 {
		return nil
	}
	return &ui.MissingEntry{
		Scope:   "tool",
		Key:     meta.Key,
		Name:    meta.Name,
		Icon:    meta.Icon,
		URL:     "/manager/tools/" + meta.Key,
		Missing: missing,
	}
}
