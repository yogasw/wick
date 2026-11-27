package agents

import (
	"context"
	"net/url"

	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
)

// TeamSessionPath is the Team app page of sessionID, relative to the app URL,
// or "" when the session is not a Team agent's own conversation. The agent's
// main chat opens as /tools/agents/team/<handle>; any other chat of it adds
// ?session=<id>. Chat channels use it so a "link to this session" command
// lands where the agent is actually used, not on the raw sessions page.
//
// Sub-agent, group and ticket-room sessions get "": the Team app does not
// list them under the agent, so the sessions page is the only place that
// opens them.
func TeamSessionPath(sessionID string) string {
	if globalTeam == nil || globalLayout.BaseDir == "" || storage.ValidateSessionID(sessionID) != nil {
		return ""
	}
	s, err := session.Load(globalLayout, sessionID)
	if err != nil {
		return ""
	}
	m := s.Meta
	if m.ParentSessionID != "" || m.GroupSessionID != "" || m.TicketRoom != nil || m.AgentGroup != nil {
		return ""
	}
	ctx := context.Background()
	agentID := teamAgentOfSession(ctx, sessionID)
	if agentID == "" {
		return ""
	}
	p, err := globalTeam.Get(ctx, agentID)
	if err != nil || p.Handle == "" {
		return ""
	}
	path := agentsToolBase + "/team/" + url.PathEscape(p.Handle)
	if m.AgentMain {
		return path
	}
	return path + "?session=" + url.QueryEscape(sessionID)
}
