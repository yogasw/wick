package team

import (
	"context"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
)

// maxParentHops bounds the walk up ParentSessionID. Delegation depth is
// capped far below this; the bound only guards against a corrupted
// parent chain looping forever.
const maxParentHops = 5

// AgentOfSession returns the agent id a session belongs to, or "" when it
// belongs to none.
//
// A sub-agent's session is created by the delegation path, which knows
// nothing about agents and leaves AgentID empty. Walking up to the parent
// is what keeps an agent's checklist on the work it hands off — without
// it, delegating would be a way out of the agent's own scope.
func AgentOfSession(layout config.Layout, sessionID string) string {
	id := sessionID
	for i := 0; i <= maxParentHops && id != ""; i++ {
		s, err := session.Load(layout, id)
		if err != nil {
			return ""
		}
		if s.Meta.AgentID != "" {
			return s.Meta.AgentID
		}
		id = s.Meta.ParentSessionID
	}
	return ""
}

// SpawnIdentity picks the wick user whose access a spawn of a session gets.
// caller is the human who triggered the turn, "" when none did (schedule,
// cron, bot, workflow); agent is the Team agent the session belongs to, nil
// for an ordinary session. "" = no identity at all.
//
// An ordinary session runs as the caller, else as its owner. An agent
// session in RunAsOwner mode always runs as the agent's owner; in
// RunAsCaller mode as the caller, else as the agent's owner. Either way
// the agent's checklist narrows the result — that is applied per request
// by the scope resolver, not here.
func SpawnIdentity(meta session.Meta, agent *entity.AgentPersona, caller string) string {
	if agent == nil {
		if caller != "" {
			return caller
		}
		return meta.UserID
	}
	if NormalizeRunAs(agent.RunAs) == RunAsOwner || caller == "" {
		return agent.OwnerUserID
	}
	return caller
}

// AgentFor returns the agent sessionID belongs to, nil for an ordinary
// session or an agent that no longer exists (its scope is deny-all, see
// ScopeForSession).
func (s *Service) AgentFor(ctx context.Context, sessionID string) *entity.AgentPersona {
	if s == nil || sessionID == "" {
		return nil
	}
	agentID := AgentOfSession(s.layout, sessionID)
	if agentID == "" {
		return nil
	}
	p, err := s.Get(ctx, agentID)
	if err != nil {
		return nil
	}
	return &p
}

// IdentityFixed reports whether a session's spawn identity does not depend
// on who triggers the turn — an agent in RunAsOwner mode — so a new caller
// is no reason to respawn it.
func (s *Service) IdentityFixed(ctx context.Context, sessionID string) bool {
	p := s.AgentFor(ctx, sessionID)
	return p != nil && NormalizeRunAs(p.RunAs) == RunAsOwner
}
