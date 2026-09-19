package api

import (
	"context"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/workflow/datatable"
	"github.com/yogasw/wick/internal/configs"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/mcp"
	"github.com/yogasw/wick/internal/pkg/adminscope"
	"github.com/yogasw/wick/internal/tags"
)

// dataTableACL gates the MCP/agent data-table ops (datatable_*) per caller,
// mirroring the /data-tables UI rule: the owner (via the owner:<slug> tag) or
// a user an admin granted that tag to may access; an admin sees all only when
// admin_see_all_sessions is on; a direct-owner fallback covers a legacy/unwired-tags
// table. Implements wfconn.DataTableACL.
//
// The caller id is the SESSION owner (resolved upstream in connectors.Service
// via SetSessionOwnerUserResolver), not the agent's shared internal token
// principal. When no session owner resolves the id stays the internal system
// principal (or empty) — both treated as unrestricted here so agent calls in
// ownerless/system contexts aren't blocked.
type dataTableACL struct {
	tags  *tags.Service
	login *login.Service
	dt    datatable.Service
	cfg   *configs.Service
}

// CanAccess reports whether userID may read/write the table with the given slug.
func (a dataTableACL) CanAccess(ctx context.Context, userID, slug string) bool {
	if userID == "" || userID == mcp.InternalAgentUserID {
		return true // internal / system principal — unrestricted
	}
	// Admin + admin_see_all_sessions → unrestricted (same knob the
	// /data-tables UI honours).
	if a.cfg != nil && adminscope.AdminSeeAllSessions(a.cfg) && a.login != nil {
		if u, err := a.login.GetUserByID(ctx, userID); err == nil && u != nil && u.IsAdmin() {
			return true
		}
	}
	// Owner or admin-granted user (owner:<slug> tag).
	if a.tags != nil {
		if owns, _ := a.tags.UserOwnsResource(ctx, userID, slug); owns {
			return true
		}
	}
	// Direct-owner fallback (tags unwired / legacy table).
	if a.dt != nil {
		if sc, err := a.dt.LoadSchema(slug); err == nil && sc.UserID != "" && sc.UserID == userID {
			return true
		}
	}
	return false
}

// RegisterOwner records userID as owner of a table it just created via the
// owner:<slug> tag. Skips the internal/system principal so agent-created
// tables in an ownerless session stay ownerless (admin-only) rather than
// owned by the synthetic agent user.
func (a dataTableACL) RegisterOwner(ctx context.Context, userID, slug string) {
	if a.tags == nil || userID == "" || userID == mcp.InternalAgentUserID || slug == "" {
		return
	}
	// Same reasoning as workflowOwnership.RegisterOwner: logged, not
	// returned — the table exists either way, but a lost owner tag must
	// leave a trace rather than surface later as "my table vanished".
	if err := a.tags.CreateResourceOwnerTag(ctx, slug, userID); err != nil {
		log.Warn().Err(err).
			Str("table", slug).
			Str("user", userID).
			Msg("data table acl: owner tag not recorded; the table stays admin-only")
	}
}
