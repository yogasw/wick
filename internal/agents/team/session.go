package team

import (
	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
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
