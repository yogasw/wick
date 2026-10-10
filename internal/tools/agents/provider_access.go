package agents

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/pkg/adminscope"
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

// adminSeeAllProviders reports whether the admin_see_all_provider_instances
// knob is on (default, and with no configs wired). Off, the admin role no
// longer walks past provider access/manage tags. A var so tests can flip it
// without a configs service.
var adminSeeAllProviders = func() bool {
	if globalConfigs == nil {
		return true
	}
	return adminscope.AdminSeeAllProviderInstances(globalConfigs)
}

// userBypassesProviderTags reports whether u's admin role alone grants
// every provider instance: an admin, while the knob is on. Every
// provider-use and provider-manage shortcut goes through this, never
// through IsAdmin directly, so turning the knob off cannot miss one.
func userBypassesProviderTags(u *entity.User) bool {
	return u != nil && u.IsAdmin() && adminSeeAllProviders()
}

// callerBypassesProviderTags is userBypassesProviderTags for the request's
// caller.
func callerBypassesProviderTags(c *tool.Ctx) bool {
	return userBypassesProviderTags(login.GetUser(c.Context()))
}

// providerPerm is the whole permission rule in one place, as data.
//
// approved   — signed in and approved at all.
// isAdmin    — holds the admin role.
// adminAll   — admin_see_all_provider_instances is on (off: tags decide).
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
func providerPerm(approved, isAdmin, adminAll, accessTag, manageTag bool) (canAccess, canManage bool) {
	if !approved {
		return false, false
	}
	if isAdmin && adminAll {
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
	return userCanAccessProvider(c.Context(), login.GetUser(c.Context()), t, name)
}

// userCanAccessProvider is canAccessProvider for a given user rather than
// the request's caller — a workflow run asks it about the workflow's
// owner, who is not whoever happens to be signed in.
func userCanAccessProvider(ctx context.Context, u *entity.User, t provider.Type, name string) bool {
	if u == nil {
		return false
	}
	if userBypassesProviderTags(u) {
		access, _ := providerPerm(u.Approved, true, true, false, false)
		return access
	}
	access, _ := providerPerm(u.Approved, u.IsAdmin(), false, providerAccessTagAllows(ctx, u, t, name), false)
	return access
}

// providerAccessTagAllows asks the tag store whether u passes the
// instance's ACCESS tags (untagged = yes). With no auth service wired it
// says no — an unwired ACL fails closed. A var so tests can stand in for
// the tag store.
//
// Not CanAccessTool: that one lets every admin through, which made
// admin_see_all_provider_instances=false a no-op for every picker. The
// admin shortcut lives in userBypassesProviderTags and nowhere else.
var providerAccessTagAllows = func(ctx context.Context, u *entity.User, t provider.Type, name string) bool {
	if globalAuth == nil {
		return false
	}
	return globalAuth.CanAccessOpenTaggedResource(ctx, u, providerAccessPath(t, name))
}

// lookupWorkflowOwner resolves a workflow owner id to the account. A var
// for the same reason as providerAccessTagAllows.
var lookupWorkflowOwner = func(ctx context.Context, id string) (*entity.User, error) {
	if globalAuth == nil {
		return nil, fmt.Errorf("auth service not wired")
	}
	return globalAuth.GetUserByID(ctx, id)
}

// workflowProviderAccess is the workflow registry's owner gate (see
// wfprovider.AccessFn): may the workflow's owner run on typ/name?
//
// An owner-less workflow is let through. That is the rule the workflow
// already lives by for its workspace — nothing refuses the run, the
// editor warns that it runs as wick's internal principal — and refusing
// here would break every unattributed (MCP/system-created) workflow on
// upgrade. An owner id naming no account fails closed: there is nobody
// left whose access the run could borrow.
func workflowProviderAccess(ctx context.Context, ownerUserID, typ, name string) error {
	if ownerUserID == "" {
		return nil
	}
	key := typ + "/" + name
	u, err := lookupWorkflowOwner(ctx, ownerUserID)
	if err != nil || u == nil {
		return fmt.Errorf("owner has no access to provider %s: workflow owner %s no longer exists", key, ownerUserID)
	}
	if !userCanAccessProvider(ctx, u, provider.Type(typ), name) {
		return fmt.Errorf("owner has no access to provider %s (provider access tags)", key)
	}
	return nil
}

// canManageProvider reports whether the caller may reconnect this
// instance or force a usage re-check. Admins always may: the Providers
// page is where they look after every account, and the
// admin_see_all_provider_instances knob only narrows what they can PICK.
// Everyone else needs an explicit tag grant on the manage path.
func canManageProvider(c *tool.Ctx, t provider.Type, name string) bool {
	u := login.GetUser(c.Context())
	if u == nil {
		return false
	}
	if u.IsAdmin() {
		_, manage := providerPerm(u.Approved, true, true, false, false)
		return manage
	}
	// The owner looks after their own instance (reconnect, usage).
	if u.Approved && providerOwnedBy(c.Context(), u, t, name) {
		return true
	}
	// Manage stands alone: the Providers menu is for whoever looks after
	// the account, and that person does not also have to be allowed to
	// pick the provider for a project.
	_, manage := providerPerm(u.Approved, u.IsAdmin(), false, true,
		providerManageTagAllows(c.Context(), u, t, name))
	return manage
}

// providerOwnedBy reports whether u carries the instance's owner tag —
// the one tagProviderOwner hands its creator. A var so tests can stand
// in for the tag store.
var providerOwnedBy = func(ctx context.Context, u *entity.User, t provider.Type, name string) bool {
	if globalTagsSvc == nil || u == nil {
		return false
	}
	ok, _ := globalTagsSvc.UserOwnsResource(ctx, u.ID, providerOwnerResource(t, name))
	return ok
}

// Owner permissions: what the owner of a provider instance (the user
// carrying its owner tag, see tagProviderOwner) may do to it. Stored per
// instance in provider.Instance.OwnerPerms, ticked by an admin on the
// instance's detail page. Admins may do all of them whatever is ticked.
const (
	ownerPermConfigure       = "configure"
	ownerPermModels          = "models"
	ownerPermEnv             = "env"
	ownerPermExtraArgs       = "extra_args"
	ownerPermBinary          = "binary"
	ownerPermExtraMCP        = "extra_mcp_servers"
	ownerPermExternalSkills  = "external_skills"
	ownerPermSandbox         = "sandbox"
	ownerPermAIRouter        = "airouter"
	ownerPermAIRouterRawConf = "airouter_raw_config"
	ownerPermAuthFrom        = "borrow_login"
	ownerPermRename          = "rename"
	ownerPermDelete          = "delete"
	ownerPermStorage         = "storage_sync"
	ownerPermRescan          = "rescan"
	ownerPermSessions        = "view_sessions"
)

// ownerPermKeys is every owner permission, in the order the detail page
// lists them.
var ownerPermKeys = []string{
	ownerPermConfigure, ownerPermModels, ownerPermEnv, ownerPermExtraArgs,
	ownerPermBinary, ownerPermExtraMCP, ownerPermExternalSkills, ownerPermSandbox,
	ownerPermAIRouter, ownerPermAIRouterRawConf, ownerPermAuthFrom,
	ownerPermRename, ownerPermDelete, ownerPermStorage, ownerPermRescan, ownerPermSessions,
}

// ownerPermDefaults is what a permission the instance does not store reads
// as. The ones that decide what runs on the host, or whose login is used,
// start off.
var ownerPermDefaults = map[string]bool{
	ownerPermConfigure:       true,
	ownerPermModels:          true,
	ownerPermEnv:             true,
	ownerPermExtraArgs:       true,
	ownerPermBinary:          false,
	ownerPermExtraMCP:        false,
	ownerPermExternalSkills:  false,
	ownerPermSandbox:         false,
	ownerPermAIRouter:        true,
	ownerPermAIRouterRawConf: false,
	ownerPermAuthFrom:        false,
	ownerPermRename:          true,
	ownerPermDelete:          true,
	ownerPermStorage:         true,
	ownerPermRescan:          true,
	ownerPermSessions:        true,
}

// isOwnerPerm reports whether perm names a known owner permission.
func isOwnerPerm(perm string) bool {
	_, ok := ownerPermDefaults[perm]
	return ok
}

// effectiveOwnerPerms is every owner permission of an instance with the
// defaults filled in for the keys it does not store.
func effectiveOwnerPerms(stored map[string]bool) map[string]bool {
	out := make(map[string]bool, len(ownerPermDefaults))
	for k, def := range ownerPermDefaults {
		if v, ok := stored[k]; ok {
			out[k] = v
		} else {
			out[k] = def
		}
	}
	return out
}

// instanceOwnerMay reads one owner permission off the instance. A var so
// tests can stand in for the instance store. An instance that cannot be
// found grants nothing.
var instanceOwnerMay = func(t provider.Type, name, perm string) bool {
	ins, err := findProviderInstanceFn(t, name)
	if err != nil {
		return false
	}
	return effectiveOwnerPerms(ins.OwnerPerms)[perm]
}

// canProviderDo reports whether the caller may do perm to this instance:
// an admin always, its owner when the instance allows perm.
func canProviderDo(c *tool.Ctx, t provider.Type, name, perm string) bool {
	u := login.GetUser(c.Context())
	if u == nil || !u.Approved {
		return false
	}
	if u.IsAdmin() {
		return true
	}
	return providerOwnedBy(c.Context(), u, t, name) && instanceOwnerMay(t, name, perm)
}

// requireProviderDo is canProviderDo for handlers.
func requireProviderDo(c *tool.Ctx, t provider.Type, name, perm string) bool {
	if canProviderDo(c, t, name, perm) {
		return true
	}
	c.JSON(http.StatusForbidden, map[string]string{"error": "no access: an admin has not allowed this for provider owners"})
	return false
}

// providerCreateTagKey names the agents config holding the tag whose holders
// may create provider instances besides admins. Empty = admins only.
const providerCreateTagKey = "provider_create_tag"

// providerCreateTagName reads that tag name. A var so tests can set it.
var providerCreateTagName = func() string {
	if globalConfigs == nil {
		return ""
	}
	return strings.TrimSpace(globalConfigs.GetOwned("agents", providerCreateTagKey))
}

// userCarriesTagNamed reports whether u carries the tag called name. A var
// so tests can stand in for the tag store.
var userCarriesTagNamed = func(ctx context.Context, u *entity.User, name string) bool {
	if globalTagsSvc == nil || u == nil {
		return false
	}
	ok, _ := globalTagsSvc.UserCarriesTagName(ctx, u.ID, name)
	return ok
}

// canCreateProvider reports whether the caller may add a provider instance:
// an admin, or a holder of the whitelist tag named in provider_create_tag.
// A non-admin creator owns what they create, and the host and login
// settings of it follow the default owner permissions (ownerPermDefaults).
func canCreateProvider(c *tool.Ctx) bool {
	u := login.GetUser(c.Context())
	if u == nil || !u.Approved {
		return false
	}
	if u.IsAdmin() {
		return true
	}
	name := providerCreateTagName()
	return name != "" && userCarriesTagNamed(c.Context(), u, name)
}

// providerKeyPerm is the owner permission a per-instance config key
// needs. The host keys (binary, flags, env, extra MCP server commands,
// skill dirs, the codex sandbox) each have their own permission, since
// they decide what runs on the host; auth_from and the account dir
// (opencode data dir, omp profile) decide whose login runs.
func providerKeyPerm(key string) string {
	switch key {
	case "binary":
		return ownerPermBinary
	case "extra_args":
		return ownerPermExtraArgs
	case "env":
		return ownerPermEnv
	case "extra_mcp_servers":
		return ownerPermExtraMCP
	case "load_external_skills":
		return ownerPermExternalSkills
	case "sandbox_mode":
		return ownerPermSandbox
	case "auth_from", "opencode_data_dir", "omp_profile":
		return ownerPermAuthFrom
	case "models", "model_select", "live_models", "live_model_filter", "live_model_default", "opencode_model":
		return ownerPermModels
	}
	return ownerPermConfigure
}

// providerManageTagAllows asks the tag store whether u passes the
// instance's MANAGE tags (untagged = no). Fails closed with no auth
// service wired. A var for the same reason as providerAccessTagAllows.
var providerManageTagAllows = func(ctx context.Context, u *entity.User, t provider.Type, name string) bool {
	if globalAuth == nil {
		return false
	}
	return globalAuth.CanAccessSharedResource(ctx, u, providerManagePath(t, name))
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

// providerKeyAllowed reports whether the caller may choose a provider key
// the CLIENT sent — "type", "type/name", optionally with the composer's
// "::model" suffix. Empty means nothing was chosen, which is not a choice
// to refuse. Spawn and switch paths call this before anything starts, so
// an instance the picker hides cannot be reached by posting its key.
func providerKeyAllowed(c *tool.Ctx, key string) bool {
	key, _ = splitProviderModel(key)
	t, name, ok := splitProviderKey(key)
	return !ok || canAccessProvider(c, t, name)
}

// requireProviderKeyAccess is providerKeyAllowed for JSON endpoints: it
// answers 403 with the key named, because the caller already knows the
// key (they sent it) and a 404 would read as a typo.
func requireProviderKeyAccess(c *tool.Ctx, key string) bool {
	if providerKeyAllowed(c, key) {
		return true
	}
	c.JSON(http.StatusForbidden, map[string]string{"error": errNoProviderAccess(key)})
	return false
}

// errNoProviderAccess is the one refusal text every spawn path uses.
func errNoProviderAccess(key string) string {
	key, _ = splitProviderModel(key)
	return "no access to provider " + strings.TrimSpace(key) + " (provider access tags)"
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
//
// Admins keep the menu whatever admin_see_all_provider_instances says: the
// page also carries the admin-only provider configuration (adding an
// instance, the gate, MCP clients), and admins manage every instance.
func HasManageableProvider(c *tool.Ctx) bool {
	if callerIsAdmin(c) {
		return true
	}
	if u := login.GetUser(c.Context()); u == nil || !u.Approved || globalAuth == nil {
		return false
	}
	// A whitelisted creator needs the page to add their first instance.
	if canCreateProvider(c) {
		return true
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
// caller may CHOOSE (pickers). Admins get the list unchanged while
// admin_see_all_provider_instances is on.
func visibleProviders[T any](c *tool.Ctx, items []T, key func(T) (provider.Type, string)) []T {
	if callerBypassesProviderTags(c) {
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
