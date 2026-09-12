package agents

import (
	"net/http"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// Provider access has two levels, and they are deliberately different
// questions with different defaults:
//
//   - ACCESS — may this person SEE this provider instance and read its
//     usage? Default: everyone who is logged in. An admin narrows it by
//     putting filter tags on the instance; remove the tags and it is open
//     to everyone again. Same rule, same table, as a connector.
//
//   - MANAGE — may this person RECONNECT the account and force a usage
//     re-check? Default: admins only. Tagging is the ONLY way to hand it
//     to someone else.
//
// Editing configuration is neither: it stays admin-only, always. A
// non-admin sees the detail page read-only and gets a plain refusal if
// they post to it, which is why these helpers never grant writes.
//
// Both levels reuse the tool_tags table that connectors, projects, data
// tables and skills already use — one tagging mechanism for the whole
// product, so an admin who understands connector sharing already
// understands this. The two paths are distinct rows, so an instance can
// be visible to everyone while only one team can reconnect it.

// providerAccessPath is the tool_tags path carrying an instance's ACCESS
// tags. Untagged = visible to everyone (login.CanAccessTool's rule).
func providerAccessPath(t provider.Type, name string) string {
	return "/providers/" + string(t) + "/" + name
}

// providerManagePath is the tool_tags path carrying an instance's MANAGE
// tags. Untagged = admins only (login.CanAccessSharedResource's rule).
//
// A separate path rather than a flag on the access row: the two answers
// have opposite defaults, and storing them together would make "no tags"
// ambiguous.
func providerManagePath(t provider.Type, name string) string {
	return providerAccessPath(t, name) + "/manage"
}

// callerIsAdmin reports whether the caller holds the admin role.
func callerIsAdmin(c *tool.Ctx) bool {
	u := login.GetUser(c.Context())
	return u != nil && u.IsAdmin()
}

// providerPerm is the whole permission rule in one place, as data.
//
// approved   — signed in and approved at all.
// isAdmin    — holds the admin role.
// accessTag  — login.CanAccessTool says yes for the access path (true
//              when the instance carries no filter tags: untagged is
//              open to everyone).
// manageTag  — login.CanAccessSharedResource says yes for the manage
//              path (false when untagged: manage is never implicit).
//
// Kept pure so the table below is a unit test rather than a comment.
func providerPerm(approved, isAdmin, accessTag, manageTag bool) (canAccess, canManage bool) {
	if !approved {
		return false, false
	}
	if isAdmin {
		return true, true
	}
	if !accessTag {
		return false, false
	}
	// Manage implies access: a grant to reconnect something the holder
	// cannot even see would be a grant nobody can use.
	return true, manageTag
}

// canAccessProvider reports whether the caller may see this instance.
//
// With no auth service wired (tests, minimal boots) only admins pass:
// an unwired ACL must fail closed, not open the page to everyone.
func canAccessProvider(c *tool.Ctx, t provider.Type, name string) bool {
	u := login.GetUser(c.Context())
	if u == nil {
		return false
	}
	if u.IsAdmin() {
		access, _ := providerPerm(u.Approved, true, false, false)
		return access
	}
	if globalAuth == nil {
		return false
	}
	access, _ := providerPerm(u.Approved, false,
		globalAuth.CanAccessTool(c.Context(), u, providerAccessPath(t, name), entity.VisibilityPrivate), false)
	return access
}

// canManageProvider reports whether the caller may reconnect this
// instance or force a usage re-check. Admins always may; everyone else
// needs an explicit tag grant on the manage path.
func canManageProvider(c *tool.Ctx, t provider.Type, name string) bool {
	u := login.GetUser(c.Context())
	if u == nil {
		return false
	}
	if u.IsAdmin() {
		_, manage := providerPerm(u.Approved, true, false, false)
		return manage
	}
	if globalAuth == nil {
		return false
	}
	_, manage := providerPerm(u.Approved, false,
		globalAuth.CanAccessTool(c.Context(), u, providerAccessPath(t, name), entity.VisibilityPrivate),
		globalAuth.CanAccessSharedResource(c.Context(), u, providerManagePath(t, name)))
	return manage
}

// requireProviderAccess writes the 404/403 itself and reports whether to
// continue. A provider the caller may not see answers 404, not 403: the
// existence of an instance is itself information.
func requireProviderAccess(c *tool.Ctx, t provider.Type, name string) bool {
	if canAccessProvider(c, t, name) {
		return true
	}
	c.JSON(http.StatusNotFound, map[string]string{"error": "provider not found"})
	return false
}

// requireProviderManage writes the refusal itself. Here 403 IS right:
// the caller can see the instance, so the honest answer is "you may look
// at this one, not act on it".
func requireProviderManage(c *tool.Ctx, t provider.Type, name string) bool {
	if canManageProvider(c, t, name) {
		return true
	}
	if !canAccessProvider(c, t, name) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "provider not found"})
		return false
	}
	c.JSON(http.StatusForbidden, map[string]string{"error": "no access: you can view this provider but not manage it"})
	return false
}

// requireProviderAdmin gates the configuration writes that stay
// admin-only however the tags are set. The message names the reason,
// because the SPA shows these fields read-only to non-admins and a bare
// "forbidden" would read as a bug.
func requireProviderAdmin(c *tool.Ctx) bool {
	if callerIsAdmin(c) {
		return true
	}
	c.JSON(http.StatusForbidden, map[string]string{"error": "no access: provider configuration is admin-only"})
	return false
}

// requireApprovedUser gates a page that is no longer admin-only but is
// still not public: any approved, logged-in user may load the shell, and
// the data endpoints behind it decide what they actually see.
func requireApprovedUser(c *tool.Ctx) bool {
	if u := login.GetUser(c.Context()); u != nil && u.Approved {
		return true
	}
	c.Error(http.StatusForbidden, "sign in to view providers")
	return false
}

// visibleProviders filters a slice of instances down to what the caller
// may see. Admins get the list unchanged.
func visibleProviders[T any](c *tool.Ctx, items []T, key func(T) (provider.Type, string)) []T {
	if callerIsAdmin(c) {
		return items
	}
	out := make([]T, 0, len(items))
	for _, it := range items {
		t, name := key(it)
		if canAccessProvider(c, t, name) {
			out = append(out, it)
		}
	}
	return out
}
