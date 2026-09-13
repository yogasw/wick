package agents

import (
	"net/http"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/pkg/tool"
)

// Provider tags answer two SEPARATE questions, and it matters that they
// are not the same question:
//
//   - MANAGE — the Providers menu. May this person open it, see this
//     instance there, read its usage, and reconnect it? Default: admins
//     only; a manage tag is the only way to hand it to someone else. No
//     manage tag on anything ⇒ the menu does not appear at all.
//
//   - ACCESS — provider CHOICE everywhere else. Which instances may this
//     person pick as the provider for a project, a session, a channel, a
//     workflow node, an agent profile? Default: everyone, narrowed by
//     tags, open again when the tags are removed — the connector rule.
//     It has nothing to do with the Providers menu.
//
// The earlier version used ACCESS for the menu, which conflated "may use
// this provider to run something" with "may look after this provider's
// account". Those are different jobs: most people need the first and
// should never see the second.
//
// Editing configuration is neither level: it stays admin-only, always. A
// non-admin manager sees the detail page read-only and gets a plain
// refusal if they post to it, which is why these helpers never grant
// writes.
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
//
//	when the instance carries no filter tags: untagged is
//	open to everyone).
//
// manageTag  — login.CanAccessSharedResource says yes for the manage
//
//	path (false when untagged: manage is never implicit).
//
// Kept pure so the table below is a unit test rather than a comment.
func providerPerm(approved, isAdmin, accessTag, manageTag bool) (canAccess, canManage bool) {
	if !approved {
		return false, false
	}
	if isAdmin {
		return true, true
	}
	// Independent by design. Someone who keeps a provider's login alive
	// does not thereby get to run projects on it, and someone allowed to
	// pick it for a project has no business in its account screen.
	return accessTag, manageTag
}

// canAccessProvider reports whether the caller may CHOOSE this instance
// (project/session/channel/workflow default provider). It says nothing
// about the Providers menu — see canManageProvider for that.
//
// With no auth service wired (tests, minimal boots) only admins pass:
// an unwired ACL must fail closed.
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
	// Manage stands alone: the Providers menu is for whoever looks after
	// the account, and that person does not also have to be allowed to
	// pick the provider for a project.
	_, manage := providerPerm(u.Approved, false, true,
		globalAuth.CanAccessSharedResource(c.Context(), u, providerManagePath(t, name)))
	return manage
}

// requireProviderAccess gates a pick-a-provider surface. A provider the
// caller may not choose answers 404, not 403: the existence of an
// instance is itself information.
func requireProviderAccess(c *tool.Ctx, t provider.Type, name string) bool {
	if canAccessProvider(c, t, name) {
		return true
	}
	c.JSON(http.StatusNotFound, map[string]string{"error": "provider not found"})
	return false
}

// requireProviderManage gates everything behind the Providers menu.
// Without the grant the instance answers 404 rather than 403 — a menu
// the caller cannot open should not enumerate what is inside it.
func requireProviderManage(c *tool.Ctx, t provider.Type, name string) bool {
	if canManageProvider(c, t, name) {
		return true
	}
	c.JSON(http.StatusNotFound, map[string]string{"error": "provider not found"})
	return false
}

// manageableProviders filters instances down to the ones the caller may
// manage — what the Providers menu shows.
func manageableProviders[T any](c *tool.Ctx, items []T, key func(T) (provider.Type, string)) []T {
	if callerIsAdmin(c) {
		return items
	}
	out := make([]T, 0, len(items))
	for _, it := range items {
		t, name := key(it)
		if canManageProvider(c, t, name) {
			out = append(out, it)
		}
	}
	return out
}

// HasManageableProvider reports whether the caller may manage ANY
// instance — the one question the sidebar asks, since a menu that opens
// onto an empty page is worse than no menu.
//
// Exported because the layout builder lives beside it and the templ view
// takes it as a plain bool.
func HasManageableProvider(c *tool.Ctx) bool {
	if callerIsAdmin(c) {
		return true
	}
	if u := login.GetUser(c.Context()); u == nil || !u.Approved || globalAuth == nil {
		return false
	}
	instances, err := provider.Load()
	if err != nil {
		return false
	}
	for _, ins := range instances {
		if canManageProvider(c, ins.Type, ins.Name) {
			return true
		}
	}
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

// requireProviderMenu gates the Providers page itself: the caller must
// manage at least one instance, or the page is not theirs to open.
func requireProviderMenu(c *tool.Ctx) bool {
	if !requireApprovedUser(c) {
		return false
	}
	if HasManageableProvider(c) {
		return true
	}
	c.Error(http.StatusForbidden, "no access: the Providers page is for provider managers")
	return false
}

// visibleProviders filters a slice of instances down to the ones the
// caller may CHOOSE (pickers). Admins get the list unchanged.
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
