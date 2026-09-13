// Package adminscope answers one question for every surface that asks it:
// how far does the admin ROLE alone see? It holds the config keys behind that
// and the readers that interpret them, and depends on nothing, so the agents
// dashboard, the data-table ACL and the connectors service can all share one
// answer without importing each other.
package adminscope

// The knobs, and why there are two of them.
//
// There used to be ONE switch, admin_see_all, and it answered two unrelated
// questions at once: may an admin read everyone's sessions, and may an admin
// use everyone's connected accounts. Granting the first silently granted the
// second, which is how an admin who wanted to look at a project ended up able
// to post to Slack as somebody else. They are separate switches now.
const (
	// ConfigOwner is the configs-table owner every Agents knob is stored under.
	ConfigOwner = "agents"

	// KeyAdminSeeAllSessions gates sessions, projects, data tables and the
	// scheduled-message monitor. Default off.
	KeyAdminSeeAllSessions = "admin_see_all_sessions"

	// KeyAdminSeeAllConnectors gates connector instances and the OAuth
	// accounts connected to them, in the dashboard and in wick_list.
	// Default ON — that is what every install had before the split, and a
	// knob nobody has written yet must not take connectors away from an admin.
	KeyAdminSeeAllConnectors = "admin_see_all_connectors"

	// legacyKeyAdminSeeAll is the pre-split name. Still read as a fallback so
	// an install that had it on keeps its session visibility after upgrading.
	legacyKeyAdminSeeAll = "admin_see_all"
)

// ConfigReader is the slice of the configs service these readers need. Kept
// as an interface so this package stays free of the configs dependency.
type ConfigReader interface {
	GetOwned(owner, key string) string
}

// AdminSeeAllSessions reports whether the admin role alone shows every
// project and session. Off by default; the legacy key wins only while the
// new one has never been written.
func AdminSeeAllSessions(r ConfigReader) bool {
	if r == nil {
		return false
	}
	if v := r.GetOwned(ConfigOwner, KeyAdminSeeAllSessions); v != "" {
		return v == "true"
	}
	return r.GetOwned(ConfigOwner, legacyKeyAdminSeeAll) == "true"
}

// AdminSeeAllConnectors reports whether the admin role alone shows every
// connector instance and every connected account. On unless explicitly
// turned off (see KeyAdminSeeAllConnectors).
func AdminSeeAllConnectors(r ConfigReader) bool {
	if r == nil {
		return true
	}
	return r.GetOwned(ConfigOwner, KeyAdminSeeAllConnectors) != "false"
}
