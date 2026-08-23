package api

import (
	"context"

	agentregistry "github.com/yogasw/wick/internal/agents/registry"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/mcp"
)

// agentToolIdentity resolves the principal an in-process (wick provider)
// agent's tool calls should run as: the human who owns the session.
//
// The CLI providers get this identity from a per-session scoped token they
// present over loopback. The in-process path has no HTTP hop and therefore
// no bearer, so the principal is resolved here and handed to the MCP
// handlers directly. Without it, every in-process agent ran as the synthetic
// admin, and connector tag filtering never applied.
//
// A zero mcp.AgentIdentity means "no resolved owner" and keeps the old
// synthetic-admin behaviour. That is the right answer for sessions with no
// owner — rows predating ownership tracking, cron and system spawns — which
// must keep working rather than lose their tools to an attribution that has
// no human to attribute to.
func agentToolIdentity(
	ctx context.Context,
	mgr *agentregistry.Manager,
	users *login.Service,
	sessionID string,
) mcp.AgentIdentity {
	if mgr == nil || users == nil || sessionID == "" {
		return mcp.AgentIdentity{}
	}
	sess, found := mgr.Registry().Session(sessionID)
	if !found || sess.Meta.UserID == "" {
		return mcp.AgentIdentity{}
	}
	user, err := users.GetUserByID(ctx, sess.Meta.UserID)
	if err != nil || user == nil || !user.Approved {
		// Owner vanished or is not approved. This returns the synthetic-admin
		// fallback, same as an ownerless session: the in-process agent keeps
		// working with full visibility rather than being stranded mid-run by
		// an account change. That is a deliberate availability choice, and it
		// is the ONE case where per-user filtering does not apply — the HTTP
		// path is stricter here and 401s instead (auth.go rejects an
		// unapproved user), because there a human can retry.
		return mcp.AgentIdentity{}
	}
	return mcp.AgentIdentity{
		User:   user,
		TagIDs: users.GetUserFilterTagIDs(ctx, user.ID),
	}
}
