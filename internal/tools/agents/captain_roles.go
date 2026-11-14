package agents

import (
	"context"
	"encoding/json"
	"errors"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/team"
	teamagents "github.com/yogasw/wick/internal/connectors/team-agents"
	"github.com/yogasw/wick/internal/entity"
)

// RolesGet implements agents.roles_get: another agent's role document.
func (o CaptainOps) RolesGet(ctx context.Context, sessionID, agent string) (any, error) {
	mgr, err := o.manager(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	p, err := o.target(ctx, mgr, agent)
	if err != nil {
		return nil, err
	}
	return rolesView(ctx, p)
}

// OwnRolesGet implements team.roles: the calling agent's own roles.
func OwnRolesGet(ctx context.Context, agentID string) (any, error) {
	if globalTeam == nil {
		return nil, errors.New("team agents are not configured on this server")
	}
	p, err := globalTeam.Get(ctx, agentID)
	if err != nil {
		return nil, err
	}
	return rolesView(ctx, p)
}

// OwnRolesSet implements team.roles_set: the calling agent changes its
// OWN roles, only on its owner's own request in this session. The owner
// of the role change is always the agent's real owner, so a co-owner
// (role owner by a rule) can use everything but never edit the roles.
func OwnRolesSet(ctx context.Context, sessionID, agentID, raw string, baseRev int, reason string) (any, error) {
	if globalTeam == nil {
		return nil, errors.New("team agents are not configured on this server")
	}
	p, err := globalTeam.Get(ctx, agentID)
	if err != nil {
		return nil, err
	}
	if !requestedByOwner(sessionID, p.OwnerUserID) {
		return nil, errors.New("only this agent's owner, asking in their own chat, can change its roles")
	}
	var doc team.RBACDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, errors.New("doc must be the role document as JSON: " + err.Error())
	}
	return saveRoles(ctx, p, doc, baseRev, p.OwnerUserID, reason)
}

// SessionRolesGet backs wick_roles: the roles of the session's own Team
// agent. Its owner sees the whole document; anyone else only their own
// role, so a guest cannot read who else has which access.
func SessionRolesGet(ctx context.Context, sessionID string) (any, error) {
	if globalTeam == nil {
		return nil, errors.New("team agents are not configured on this server")
	}
	agentID := TeamCallerOf(ctx, sessionID)
	if agentID == "" {
		return nil, errors.New("roles are only available in a Team agent's session")
	}
	p, err := globalTeam.Get(ctx, agentID)
	if err != nil {
		return nil, err
	}
	asker := askerOf(sessionID)
	doc, _, err := globalTeam.RBAC(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	role := doc.RoleFor(p.OwnerUserID, asker, func() bool {
		_, err := globalTeam.ShareOf(ctx, p.ID, asker)
		return err == nil
	})
	enabled := team.NormalizeAccessMode(p.AccessMode) == team.AccessRoles
	if !enabled {
		// Outside By role every caller runs with the agent's normal access.
		role = ""
	}
	if asker != p.OwnerUserID || !requestedByOwner(sessionID, p.OwnerUserID) {
		return map[string]any{"agent": "@" + p.Handle, "enabled": enabled, "your_role": role,
			"note": "only the agent's owner sees the whole role document and can change it"}, nil
	}
	view, err := rolesView(ctx, p)
	if err != nil {
		return nil, err
	}
	m := view.(map[string]any)
	m["your_role"] = team.RoleOwner
	return m, nil
}

// SessionRolesSet backs wick_roles_set: the session's own Team agent
// changes its roles, on its owner's own request only.
func SessionRolesSet(ctx context.Context, sessionID, raw string, baseRev int, reason string) (any, error) {
	agentID := TeamCallerOf(ctx, sessionID)
	if agentID == "" {
		return nil, errors.New("roles are only available in a Team agent's session")
	}
	return OwnRolesSet(ctx, sessionID, agentID, raw, baseRev, reason)
}

// askerOf is the wick user who sent the session's latest turn: the
// platform sender when there is one, else the session's own user for a
// turn typed in the web UI; "" when unknown (a guest).
func askerOf(sessionID string) string {
	if globalMgr == nil || globalLayout == (agentconfig.Layout{}) {
		return ""
	}
	sess, ok := globalMgr.Registry().Session(sessionID)
	if !ok {
		return ""
	}
	turn, found, err := lastUserTurn(globalLayout.SessionConversation(sessionID))
	switch {
	case err != nil:
		return ""
	case !found:
		return sess.Meta.UserID
	case turn.Sender != nil:
		return turn.Sender.WickUserID
	case turn.Source == "ui":
		return sess.Meta.UserID
	}
	return ""
}

// rolesView is an agent's role document, rev and recent changes.
func rolesView(ctx context.Context, p entity.AgentPersona) (any, error) {
	doc, rev, err := globalTeam.RBAC(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	hist, err := globalTeam.RBACHistory(ctx, p.ID, 5)
	if err != nil {
		return nil, err
	}
	changes := make([]map[string]any, 0, len(hist))
	for _, h := range hist {
		changes = append(changes, map[string]any{"rev": h.Rev, "by": h.UpdatedBy, "reason": h.Reason, "at": h.At})
	}
	return map[string]any{
		"agent":   "@" + p.Handle,
		"enabled": team.NormalizeAccessMode(p.AccessMode) == team.AccessRoles,
		"rev":     rev,
		"doc":     doc,
		"history": changes,
	}, nil
}

// RolesSet implements agents.roles_set. Only the owner's own request in
// this chat changes roles: a teammate, a schedule or a forwarded message
// could otherwise hand someone a role.
func (o CaptainOps) RolesSet(ctx context.Context, sessionID string, in teamagents.RolesSetInput) (any, error) {
	mgr, err := o.manager(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if !requestedByOwner(sessionID, mgr.OwnerUserID) {
		return nil, errors.New("only the owner of the Team, asking in their own chat, can change an agent's roles")
	}
	p, err := o.target(ctx, mgr, in.Agent)
	if err != nil {
		return nil, err
	}
	return saveRoles(ctx, p, in.Doc, in.BaseRev, mgr.OwnerUserID, in.Reason)
}

// saveRoles stores doc as p's next role revision and drops cached scopes.
func saveRoles(ctx context.Context, p entity.AgentPersona, doc team.RBACDoc, baseRev int, by, reason string) (any, error) {
	rev, err := globalTeam.SetRBAC(ctx, p.ID, doc, baseRev, by, reason)
	if err != nil {
		return nil, err
	}
	globalTeam.Invalidate(p.ID)
	out := map[string]any{"status": "applied", "agent": "@" + p.Handle, "rev": rev}
	if team.NormalizeAccessMode(p.AccessMode) != team.AccessRoles {
		out["note"] = "saved, but this agent is not in By role access yet: the owner turns it on in Settings > Access"
	}
	return out, nil
}
