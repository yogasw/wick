package agents

import (
	"context"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/gate"
	"github.com/yogasw/wick/internal/agents/store"
	"github.com/yogasw/wick/internal/agents/team"
)

// SessionTurnRole backs pool.Config.TurnRole: before a person's message
// reaches a Team agent in By role, it writes the native tools that
// person's role may not use where the gate hook reads them. Outside By
// role, or for a role without limits, the file is removed. Mid-turn
// (working) the new limits are added to the ones in force, never
// loosened: the running turn now carries both people's words.
func SessionTurnRole(ctx context.Context, sessionID string, sender *store.Sender, working bool) {
	app := gate.AppName()
	deny, role, ok := sessionDeniedTools(ctx, sessionID, sender)
	if !ok {
		if !working {
			_ = gate.RemoveSessionRole(app, sessionID)
		}
		return
	}
	if working {
		if cur, has, err := gate.LoadSessionRole(app, sessionID); err == nil && has {
			deny = gate.MergeDeny(cur.Deny, deny)
			if cur.Role != role {
				role = cur.Role + "+" + role
			}
		}
	}
	if err := gate.WriteSessionRole(app, sessionID, gate.SessionRole{Role: role, Deny: deny}); err != nil {
		log.Ctx(ctx).Warn().Err(err).Str("session", sessionID).Msg("roles: could not write the session's tool limits")
	}
}

// sessionDeniedTools resolves who sent the message and what their role
// denies. ok is false when the session is not a Team agent's in By role.
func sessionDeniedTools(ctx context.Context, sessionID string, sender *store.Sender) (deny []string, role string, ok bool) {
	if globalTeam == nil {
		return nil, "", false
	}
	agentID := TeamCallerOf(ctx, sessionID)
	if agentID == "" {
		return nil, "", false
	}
	p, err := globalTeam.Get(ctx, agentID)
	if err != nil || team.NormalizeAccessMode(p.AccessMode) != team.AccessRoles {
		return nil, "", false
	}
	doc, _, err := globalTeam.RBAC(ctx, p.ID)
	if err != nil {
		// Unreadable roles: treat the sender as a guest, never as unlimited.
		doc = team.RBACDoc{}
	}
	asker := senderUserID(sessionID, sender)
	role = doc.RoleFor(p.OwnerUserID, asker, func() bool {
		_, err := globalTeam.ShareOf(ctx, p.ID, asker)
		return err == nil
	}, func(tagID string) bool { return globalTeam.UserHasTag(ctx, asker, tagID) })
	return doc.DeniedTools(role), role, true
}

// senderUserID is the wick user behind a message: the channel's sender,
// or the session's own user when no sender came with it (a schedule the
// session's user set up). A sender with no wick account is nobody: guest.
func senderUserID(sessionID string, sender *store.Sender) string {
	if sender != nil {
		return sender.WickUserID
	}
	if globalMgr == nil {
		return ""
	}
	if sess, ok := globalMgr.Registry().Session(sessionID); ok {
		return sess.Meta.UserID
	}
	return ""
}
