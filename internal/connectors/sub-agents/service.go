package subagents

import (
	"context"
	"errors"
	"strings"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// Deps is everything the ops need.
//
// Service is resolved through a function rather than held directly
// because of boot ordering: the connector must be registered before
// connectorsSvc.Bootstrap so its fixed instance is seeded in the same
// pass as every other connector, but the delegation service is
// constructed later. Late binding keeps that a single registration
// instead of a register-then-replace dance whose correctness would
// depend on the ops being identical across both passes.
//
// A nil result means delegation is not configured on this deployment;
// every op then fails with a clear message rather than a nil dereference.
type Deps struct {
	Service func() *delegation.Service
	Layout  agentconfig.Layout
}

// svc resolves the delegation service, or nil when unavailable.
func (d Deps) svc() *delegation.Service {
	if d.Service == nil {
		return nil
	}
	return d.Service()
}

var (
	errUnavailable     = errors.New("sub-agent delegation is not configured on this server")
	errNotAuthenticated = errors.New("not authenticated")
	errNoSession        = errors.New("cannot resolve the calling session — sub-agent operations are only available to a running agent session")
)

// caller bundles the resolved identity and scope for one op.
//
// Every field is derived from the framework, never from op input. The
// session in particular comes from the per-spawn header (see
// connector.Ctx.SessionID) — a session_id the model could name would let
// a caller attach work to someone else's conversation, inherit its
// identity for tag purposes, and read roles from its project.
type caller struct {
	user      *entity.User
	sessionID string
	projectID string
	tagIDs    []string
}

func (d Deps) ready() error {
	if s := d.svc(); s == nil || s.Repo == nil {
		return errUnavailable
	}
	return nil
}

// resolveCaller establishes who is calling and in which scope.
//
// requireSession is false for ops that are meaningful without a
// conversation (listing roles from the admin test panel); it is true for
// anything that attaches to a tree or a board, where an unresolvable
// session would silently act as somebody else.
func (d Deps) resolveCaller(ctx context.Context, sessionID string, requireSession bool) (caller, error) {
	if err := d.ready(); err != nil {
		return caller{}, err
	}
	u := login.GetUser(ctx)
	if u == nil {
		return caller{}, errNotAuthenticated
	}
	c := caller{user: u, sessionID: strings.TrimSpace(sessionID)}
	if c.sessionID == "" {
		if requireSession {
			return caller{}, errNoSession
		}
		c.tagIDs = d.userTags(ctx, u.ID)
		return c, nil
	}

	// The session's OWNER is the human this tree is accountable to, which
	// is who tag inheritance and interrupt authorisation must key on — not
	// whoever happens to be driving the transport.
	ownerID := u.ID
	if sess, err := session.Load(d.Layout, c.sessionID); err == nil {
		c.projectID = sess.Meta.ProjectID
		if sess.Meta.UserID != "" {
			ownerID = sess.Meta.UserID
		}
	}
	c.tagIDs = d.userTags(ctx, ownerID)
	return c, nil
}

func (d Deps) userTags(ctx context.Context, userID string) []string {
	s := d.svc()
	if s == nil || s.Tags == nil || userID == "" {
		return nil
	}
	return s.Tags.GetUserFilterTagIDs(ctx, userID)
}

// visibleRoles returns the roles this caller may delegate to, in their
// project scope. Scope decides what exists; tags decide what may be seen.
func (d Deps) visibleRoles(ctx context.Context, c caller) ([]entity.AgentProfile, error) {
	rows, err := d.svc().Repo.ListProfilesInScopes(ctx, c.projectID, false)
	if err != nil {
		return nil, err
	}
	resolved := delegation.ResolveScoped(rows, c.projectID)
	return delegation.VisibleProfiles(resolved, c.tagIDs, c.user.IsAdmin()), nil
}
