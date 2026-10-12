package agents

import (
	"context"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/schedule"
	"github.com/yogasw/wick/internal/agents/team"
)

// RoomGuardScheduleSender is next as the scheduler sees it, refusing a
// fire into a ticket room or a member's backing session: those run only
// through the room's own send, which scopes them to the ticket.
func RoomGuardScheduleSender(next schedule.Sender, layout agentconfig.Layout) schedule.Sender {
	return roomGuardSender{Sender: next, layout: layout}
}

type roomGuardSender struct {
	schedule.Sender
	layout agentconfig.Layout
}

func (g roomGuardSender) SendWithProject(ctx context.Context, sessionID, agentName, source, role, text, projectID string) error {
	if err := team.RefuseRoomSession(g.layout, sessionID); err != nil {
		return err
	}
	return g.Sender.SendWithProject(ctx, sessionID, agentName, source, role, text, projectID)
}
