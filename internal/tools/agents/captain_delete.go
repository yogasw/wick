package agents

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yogasw/wick/internal/agents/team"
	teamagents "github.com/yogasw/wick/internal/connectors/team-agents"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
)

// Delete implements agents.delete: the Captain removes (or turns off)
// another agent of its owner's Team, the way Settings › Delete does. It
// is guarded harder than the other ops, since nothing brings an agent
// back: the person of the asking chat must be the owner (or an admin),
// the handle is repeated as a confirmation, a Captain is never removed,
// and an agent with a chat running or queued is refused until idle.
func (o CaptainOps) Delete(ctx context.Context, sessionID string, in teamagents.DeleteInput) (any, error) {
	mgr, err := o.manager(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if who := sessionUserOf(sessionID); who != mgr.OwnerUserID {
		if u := login.GetUser(ctx); u == nil || !u.IsAdmin() {
			return nil, errors.New("only the owner of the Team (or an admin) can have an agent deleted: ask them in their own chat")
		}
	}
	p, err := o.target(ctx, mgr, in.Agent)
	if err != nil {
		return nil, err
	}
	if team.NormalizeHandle(in.Confirm) != p.Handle {
		return nil, fmt.Errorf("confirm must repeat the handle of the agent to remove (@%s)", p.Handle)
	}
	if p.IsCaptain {
		return nil, fmt.Errorf("@%s is a Captain: the owner makes another agent Captain first, in Settings", p.Handle)
	}
	if busy := busyAgentChats(p); len(busy) > 0 {
		return nil, fmt.Errorf("@%s has %d chat(s) running or queued (%s): try again once it is idle", p.Handle, len(busy), strings.Join(busy, ", "))
	}
	mode := in.Mode
	if mode == "" {
		mode = "delete"
	}
	switch mode {
	case "disable":
		if p.Disabled {
			return map[string]any{"status": "unchanged", "agent": "@" + p.Handle, "disabled": true}, nil
		}
		p.Disabled = true
		if err := globalTeam.Update(ctx, &p); err != nil {
			return nil, err
		}
		return map[string]any{"status": "disabled", "agent": "@" + p.Handle, "note": "turned off; the owner can turn it on again in its Settings"}, nil
	case "delete":
	default:
		return nil, fmt.Errorf("mode is delete or disable, not %q", in.Mode)
	}
	rows, err := globalTeam.List(ctx, mgr.OwnerUserID)
	if err != nil {
		return nil, err
	}
	if err := globalTeam.Delete(ctx, p.ID); err != nil {
		return nil, err
	}
	if err := afterTeamAgentDelete(ctx, p, rows, in.PurgeChats); err != nil {
		return nil, err
	}
	kept := "its project and chats are kept as an ordinary project"
	if in.PurgeChats {
		kept = "its project and chats were deleted"
	}
	return map[string]any{"status": "deleted", "agent": "@" + p.Handle, "project_id": p.ProjectID, "note": kept}, nil
}

// busyAgentChats lists the agent's chats a turn runs or waits in.
func busyAgentChats(p entity.AgentPersona) []string {
	_, sessions := agentScheduleScope(p)
	var busy []string
	for _, s := range sessions {
		if poolSessionBusy(s) {
			busy = append(busy, s)
		}
	}
	return busy
}
