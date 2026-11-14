package agents

import (
	"context"
	"errors"

	"github.com/yogasw/wick/internal/agents/team"
	teamagents "github.com/yogasw/wick/internal/connectors/team-agents"
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
	rev, err := globalTeam.SetRBAC(ctx, p.ID, in.Doc, in.BaseRev, mgr.OwnerUserID, in.Reason)
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
