package api

import (
	"context"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	agentpool "github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/agents/team"
)

// roomGuardSender is the pool as the scheduler sees it, refusing a fire
// into a ticket room or a member's backing session: those run only
// through the room's own send, which scopes them to the ticket.
type roomGuardSender struct {
	*agentpool.Pool
	layout agentconfig.Layout
}

func (g roomGuardSender) SendWithProject(ctx context.Context, sessionID, agentName, source, role, text, projectID string) error {
	if err := team.RefuseRoomSession(g.layout, sessionID); err != nil {
		return err
	}
	return g.Pool.SendWithProject(ctx, sessionID, agentName, source, role, text, projectID)
}
