package view

import (
	"fmt"

	"github.com/yogasw/wick/internal/entity"
)

func shortID(id string) string {
	if len(id) > 11 {
		return id[:4] + "…" + id[len(id)-4:]
	}
	return id
}

// viewerEmail is the signed-in user's email for the Team account menu, or
// "" when nobody is signed in.
func viewerEmail(u *entity.User) string {
	if u == nil {
		return ""
	}
	return u.Email
}

// viewerAvatar is the signed-in user's picture URL for the Team account
// row, so it shows the same avatar as the Agents sidebar; "" falls back
// to the initial.
func viewerAvatar(u *entity.User) string {
	if u == nil {
		return ""
	}
	return u.Avatar
}

// sidebarResizeCSS is the <style> behind sidebarResize: the sidebar takes
// --wick-sidebar-w on desktop (the saved width when there is one, else the
// space's default), and the drag handle on its right edge. The handle's base rule hides it and
// must come before the @media rule that shows it on desktop. Built here rather
// than with Tailwind classes so no build step has to know about them.
func sidebarResizeCSS(space string, width int) string {
	sel, bp, def := "aside[data-space-sidebar]", 1024, "300px"
	if space == "agents" {
		sel, bp, def = "[data-sidebar][data-space-sidebar]", 768, "15rem"
	}
	saved := ""
	if w := entity.ClampSidebarWidth(width); w > 0 {
		saved = fmt.Sprintf(":root{--wick-sidebar-w:%dpx}", w)
	}
	return fmt.Sprintf(`<style>%s
[data-sidebar-resize]{display:none;position:absolute;top:0;bottom:0;right:-4px;width:8px;cursor:col-resize;z-index:20;touch-action:none}
@media (min-width:%dpx){%s{width:var(--wick-sidebar-w,%s)}[data-sidebar-resize]{display:block}}
[data-sidebar-resize]::after{content:"";position:absolute;top:0;bottom:0;left:3px;width:2px;background:#22c55e;opacity:0;transition:opacity 120ms}
[data-sidebar-resize]:hover::after,html.wick-sidebar-resizing [data-sidebar-resize]::after{opacity:1}
html.wick-sidebar-resizing,html.wick-sidebar-resizing *{cursor:col-resize!important;user-select:none!important}
</style>`, saved, bp, sel, def)
}

// sidebarWidth is the width the signed-in user saved for space, or 0.
func sidebarWidth(u *entity.User, space string) int {
	if u == nil {
		return 0
	}
	if space == "team" {
		return u.Metadata.Sidebar.Team
	}
	return u.Metadata.Sidebar.Agents
}
