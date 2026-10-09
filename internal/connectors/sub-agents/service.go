package subagents

import (
	"context"
	"encoding/json"
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
	// TeamChats (optional) is, for a Team agent's session, the chat
	// paired with it at each teammate; nil for none. list_agents shows it
	// as team_chats.
	TeamChats func(ctx context.Context, sessionID string) any
}

// svc resolves the delegation service, or nil when unavailable.
func (d Deps) svc() *delegation.Service {
	if d.Service == nil {
		return nil
	}
	return d.Service()
}

var (
	errUnavailable      = errors.New("sub-agent delegation is not configured on this server")
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
	// providerKey and modelID are what the CALLING conversation is running
	// on, read off its active agent entry. A role created without an
	// explicit provider adopts these, so an agent asked to "make me a
	// sub-agent" produces one that runs where the conversation runs instead
	// of on whatever instance is configured as the global default.
	//
	// The two always travel together: a model id is scoped to the instance
	// it was chosen on.
	providerKey string
	modelID     string
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
	// Read separately from the meta above: this comes off the active AGENT
	// entry, not meta.json, and is what a new role inherits when it names no
	// provider of its own.
	if prov, model, ok := session.ActiveRunTarget(d.Layout, c.sessionID); ok {
		c.providerKey, c.modelID = prov, model
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

// errNoTree means the calling conversation has no delegation tree yet, so
// there is nobody to address.
var errNoTree = errors.New("no other agents are working in this conversation yet — delegate one first")

// treePosition resolves the calling session to its place in a delegation
// tree: the tree's root id and the caller's own handle.
//
// Both come from the SESSION, never from op input. A model that could
// name its own handle could claim to be the leader, and one that could
// name a root could post into a tree it has no part in.
func (d Deps) treePosition(ctx context.Context, sessionID string) (rootID, handle string, err error) {
	if sessionID == "" {
		return "", "", errNoSession
	}
	repo := d.svc().Repo
	// A sub-agent: its own row carries both answers.
	if row, ferr := repo.FindByChildSession(ctx, sessionID); ferr == nil && row != nil {
		if row.Handle == "" {
			// Pre-dates handle allocation. Addressable neither way, and
			// saying so beats messaging into the void.
			return "", "", errNoTree
		}
		return row.RootID, row.Handle, nil
	} else if ferr != nil {
		return "", "", ferr
	}

	// A leader: it owns the trees it started. Any delegation it launched
	// names the same root, so the first one is enough.
	rows, lerr := repo.ListByParent(ctx, sessionID)
	if lerr != nil {
		return "", "", lerr
	}
	for _, r := range rows {
		if r.RootID != "" {
			return r.RootID, entity.LeaderHandle, nil
		}
	}
	return "", "", errNoTree
}

// addressedTree is treePosition for an op that names a recipient: the
// tree that recipient lives in.
//
// A sub-agent addresses its own tree, so nothing changes for it. A leader
// is different: each top-level delegate starts its OWN tree, so a leader
// owns many, and treePosition's "first tree is enough" picks only the most
// recent one. A message to an agent started earlier in the conversation —
// still running, still listed by list_agents — then came back "not part of
// this conversation". The leader's recipient is found among its own direct
// children instead, and its tree is the one the message goes to.
func (d Deps) addressedTree(ctx context.Context, sessionID, handle string) (rootID, from string, err error) {
	root, from, err := d.treePosition(ctx, sessionID)
	if err != nil || from != entity.LeaderHandle {
		return root, from, err
	}
	rows, err := d.svc().Repo.ListByParent(ctx, sessionID)
	if err != nil {
		return "", "", err
	}
	if t := leaderChild(rows, handle); t != nil && t.RootID != "" {
		return t.RootID, from, nil
	}
	return root, from, nil
}

// leaderChild picks the leader's sub-agent answering to handle. Handles
// are unique within a tree, not across a leader's trees, so the same role
// delegated twice yields two rows with one handle: the one still working
// wins, else the newest (rows arrive newest first).
func leaderChild(rows []entity.AgentDelegation, handle string) *entity.AgentDelegation {
	var newest *entity.AgentDelegation
	for i := range rows {
		r := &rows[i]
		if r.Handle != handle {
			continue
		}
		if !entity.IsTerminalDelegationStatus(r.Status) {
			return r
		}
		if newest == nil {
			newest = r
		}
	}
	return newest
}

// callerMayDelegate reports whether the calling session is allowed to
// delegate or define roles.
//
// This is what makes `can_delegate` a real setting rather than a stored
// boolean nobody reads. A LEADER session (a human's conversation) always
// may; a SUB-AGENT may only when its own role opted in. Without the
// check, a role marked "cannot delegate" still could, and the field in
// the editor would be decoration.
func (d Deps) callerMayDelegate(ctx context.Context, sessionID string) (bool, string, error) {
	repo := d.svc().Repo
	row, err := repo.FindByChildSession(ctx, sessionID)
	if err != nil {
		return false, "", err
	}
	if row == nil {
		return true, "", nil // a human's own conversation
	}
	// Scope-exact by the role's own project, matching how the delegation
	// resolved it in the first place.
	profile, err := repo.GetProfileScoped(ctx, row.ProjectID, row.ProfileKey)
	if err != nil || profile == nil {
		// Unresolvable role: allow rather than strand a running tree on a
		// lookup failure. The governor's depth and budget caps still hold.
		return true, "", nil //nolint:nilerr // deliberate fail-open, see comment
	}
	if profile.CanDelegate {
		return true, "", nil
	}
	return false, profile.Key, nil
}

// splitList parses a comma-separated op input into trimmed, non-empty
// items. Comma-separated rather than a JSON array because these arrive
// from a model typing a flat string field.
func splitList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// encodeTagIDs renders a tag id list for the profile column. Empty stays
// "[]", which the ACL reads as "inherit the caller's tags in full" — not
// as "no access".
func encodeTagIDs(ids []string) string {
	if len(ids) == 0 {
		return "[]"
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return "[]"
	}
	return string(b)
}
