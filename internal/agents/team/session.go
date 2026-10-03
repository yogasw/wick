package team

import (
	"context"
	"errors"
	"os"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/entity"
)

// maxParentHops bounds the walk up ParentSessionID. Delegation depth is
// capped far below this; the bound only guards against a corrupted
// parent chain looping forever.
const maxParentHops = 5

// ErrBrokenChain is AgentOfSession's answer when the walk up the parent
// chain cannot finish: a parent that cannot be read, or a chain longer
// than maxParentHops. The session may well be an agent's sub-agent, so
// callers must treat it as deny-all, never as "not an agent".
var ErrBrokenChain = errors.New("team: session parent chain is broken")

// AgentOfSession returns the agent id a session belongs to, or "" when it
// belongs to none.
//
// A sub-agent's session is created by the delegation path, which knows
// nothing about agents and leaves AgentID empty. Walking up to the parent
// is what keeps an agent's checklist on the work it hands off — without
// it, delegating would be a way out of the agent's own scope.
//
// Fail-closed: only the session itself being absent (or not a valid id)
// reads as "no agent". Any failure past that — a parent deleted or
// unreadable mid-chain — returns ErrBrokenChain, because the missing link
// may be exactly the agent session whose scope should apply.
func AgentOfSession(layout config.Layout, sessionID string) (string, error) {
	id := sessionID
	for i := 0; i <= maxParentHops; i++ {
		if id == "" {
			return "", nil
		}
		s, err := session.Load(layout, id)
		if err != nil {
			if i == 0 && (errors.Is(err, os.ErrNotExist) || storage.ValidateSessionID(id) != nil) {
				return "", nil
			}
			return "", ErrBrokenChain
		}
		if s.Meta.AgentID != "" {
			return s.Meta.AgentID, nil
		}
		id = s.Meta.ParentSessionID
	}
	if id == "" {
		return "", nil
	}
	return "", ErrBrokenChain
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
	// A broken chain is nil here too; its scope is still deny-all (see
	// ScopeForSession), so only the spawn identity falls back.
	agentID, err := AgentOfSession(s.layout, sessionID)
	if err != nil || agentID == "" {
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
