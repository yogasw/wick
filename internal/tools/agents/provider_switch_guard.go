package agents

import (
	"context"
	"errors"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/pkg/tool"
)

func init() {
	provider.SwitchGuard = sessionSwitchGuard
	provider.FailoverGuard = sessionFailoverGuard
}

// agentForbidsSwitch reports whether sess is a Team agent's chat whose
// agent keeps its provider (allow_provider_switch off). Any origin
// counts: a Slack chat of the agent is held to the same setting.
func agentForbidsSwitch(ctx context.Context, sess session.Session) bool {
	if globalTeam == nil || sess.Meta.AgentID == "" {
		return false
	}
	p, err := globalTeam.Get(ctx, sess.Meta.AgentID)
	if err != nil {
		return false
	}
	return !team.AllowsProviderSwitch(p.AllowProviderSwitch, p.IsCaptain)
}

// sessionSwitchGuard is provider.SwitchGuard: the gates of the switch
// endpoint for a switch nobody signed in asked for — a "#tag" message
// from a channel, agentctl. The agent's own setting first, then the
// session owner's provider access (there is no caller to ask). A session
// with no owner is let through, as an owner-less workflow is.
func sessionSwitchGuard(ctx context.Context, sessionID, tag string) error {
	if globalMgr == nil {
		return nil
	}
	sess, ok := globalMgr.Registry().Session(sessionID)
	if !ok {
		return nil
	}
	if agentForbidsSwitch(ctx, sess) {
		return errors.New(errProviderSetByAgent)
	}
	return ownerProviderAccess(ctx, sess, tag)
}

// sessionFailoverGuard is provider.FailoverGuard: only provider access.
// The agent's "keeps its provider" setting is for people picking a
// provider; a failover runs the chain the owner set on the instance, so a
// kept-provider agent must not stall on a spent account. In a Team
// agent's chat the access checked is the AGENT OWNER's: the agent runs on
// the owner's providers, so whoever wrote in the channel does not narrow
// where it may fail over to.
func sessionFailoverGuard(ctx context.Context, sessionID, tag string) error {
	if globalMgr == nil {
		return nil
	}
	sess, ok := globalMgr.Registry().Session(sessionID)
	if !ok {
		return nil
	}
	userID := sess.Meta.UserID
	if globalTeam != nil && sess.Meta.AgentID != "" {
		if p, err := globalTeam.Get(ctx, sess.Meta.AgentID); err == nil && p.OwnerUserID != "" {
			userID = p.OwnerUserID
		}
	}
	return userProviderAccess(ctx, userID, tag)
}

// ownerProviderAccess refuses tag when the session's owner may not use
// that provider. A session with no owner is let through.
func ownerProviderAccess(ctx context.Context, sess session.Session, tag string) error {
	return userProviderAccess(ctx, sess.Meta.UserID, tag)
}

// userProviderAccess refuses tag when user userID may not use that
// provider. No user is let through, as an owner-less workflow is.
func userProviderAccess(ctx context.Context, userID, tag string) error {
	if userID == "" {
		return nil
	}
	key, _ := splitProviderModel(tag)
	t, name, ok := splitProviderKey(key)
	if !ok {
		return nil
	}
	u, err := lookupWorkflowOwner(ctx, userID)
	if err != nil || u == nil || !userCanAccessProvider(ctx, u, t, name) {
		return errors.New(errNoProviderAccess(tag))
	}
	return nil
}

// uiSwitchRefusal is the reason a "#tag" message in the UI may not switch
// sess, or "" when it may: the agent's setting, then the caller's
// provider access, as on the switch endpoint.
func uiSwitchRefusal(c *tool.Ctx, sess session.Session, tag string) string {
	if agentForbidsSwitch(c.Context(), sess) {
		return errProviderSetByAgent
	}
	if !providerKeyAllowed(c, tag) {
		return errNoProviderAccess(tag)
	}
	return ""
}
