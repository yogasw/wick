package admin

import "strings"

// How each surface decides who reaches it.
//
// The admin pages all write the same tool_tags table, which made it look like
// one rule. It is not — the readers differ, and two of them differ in the way
// that matters most: an UNTAGGED project or data table is NOT public, it stays
// private to its owner (login.CanAccessSharedResource, and the comment there
// says so in as many words). Rendering those as "Public 30" said the exact
// opposite of the truth.
//
// The reader is named on every row so the next person can check the claim
// rather than trust this table.
type accessRule struct {
	// UntaggedIsPublic: with no filter tag, every approved user reaches it.
	// False means untagged is owner-only, not open.
	UntaggedIsPublic bool
	// OwnerScoped: the owner is always in reach, and an "owner:<id>" tag is
	// how an admin hands ownership to somebody else.
	OwnerScoped bool
	// FilterTagsGrant: whether the filter tags on this path are actually
	// consulted by the code that decides visibility. False is not a style
	// choice — it means the tag picker on that admin page grants nothing.
	FilterTagsGrant bool
	// AdminKnob names the config that gates the admin bypass, "" when admins
	// always bypass on this surface.
	AdminKnob string
	// Reader is the function that enforces this rule, for anyone verifying.
	Reader string
}

const (
	knobConnectors = "admin_see_all_connectors"
	knobSessions   = "admin_see_all_sessions"
)

var accessRules = map[string]accessRule{
	// login.CanAccessTool: public visibility short-circuits, admins bypass
	// before tags are read, untagged private = every approved user.
	"tools": {UntaggedIsPublic: true, FilterTagsGrant: true, Reader: "login.CanAccessTool"},
	"jobs":  {UntaggedIsPublic: true, FilterTagsGrant: true, Reader: "login.CanAccessTool"},

	// canAccessProvider → CanAccessTool on the access path. Admins always.
	"providers": {UntaggedIsPublic: true, FilterTagsGrant: true, Reader: "canAccessProvider"},

	// connectors.Repo.ListAccessibleTo: untagged row visible to everyone;
	// the admin bypass is behind its own knob.
	"connectors":         {UntaggedIsPublic: true, FilterTagsGrant: true, AdminKnob: knobConnectors, Reader: "connectors.Repo.ListAccessibleTo"},
	"connector-accounts": {FilterTagsGrant: true, AdminKnob: knobConnectors, Reader: "connectors.AccountVisibleTo"},

	// The untagged-is-private pair. CanAccessSharedResource returns false for
	// an untagged path ON PURPOSE so a project does not leak to every
	// authenticated user; the owner is unioned in by the caller.
	"projects":    {OwnerScoped: true, FilterTagsGrant: true, AdminKnob: knobSessions, Reader: "login.CanAccessSharedResource"},
	"data-tables": {OwnerScoped: true, FilterTagsGrant: true, AdminKnob: knobSessions, Reader: "login.CanAccessSharedResource"},

	// Owner tag ONLY. Neither reader ever looks at the filter tags these
	// admin pages write, so a tag added there grants nothing — see
	// FilterTagsGrant and the warning the modal renders because of it.
	"workflows": {OwnerScoped: true, Reader: "spa_workflows.go listWorkflows → UserOwnsResource"},
	"skills":    {OwnerScoped: true, Reader: "skills.go skillOwned → UserOwnsResource"},

	// Connector TYPE tags are categories (is_filter=false) for grouping on
	// the connectors index — isConnectorVisibleTo never reads tags at all.
	"manager": {UntaggedIsPublic: true, Reader: "manager.isConnectorVisibleTo"},
}

// ruleFor returns the rule for a tool_path. An unknown namespace gets the
// safest reading — owner-scoped, no implicit public — so a surface added
// later cannot silently render as wide open.
func ruleFor(path string) accessRule {
	ns := strings.SplitN(strings.TrimPrefix(path, "/"), "/", 2)[0]
	if r, ok := accessRules[ns]; ok {
		return r
	}
	return accessRule{OwnerScoped: true, Reader: "unknown surface"}
}
