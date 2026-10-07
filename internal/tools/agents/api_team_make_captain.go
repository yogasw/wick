package agents

import (
	"net/http"

	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// Captain is a role the owner moves between their agents ("Make Captain"
// in Settings › Captain), not a fixed agent: the flag, the "Manage other
// agents" choice and the Captain part of the spawn prompt
// (team.CaptainRoleAddon) move; name, handle, persona, chats and memory
// stay where they are.

// Why an agent cannot take the Captain role.
const (
	captainBlockRemote = "A remote agent can't be the Captain — the Captain is always a Wick agent."
	captainBlockShared = "This agent is shared. The Captain runs your Team and can't be shared — stop sharing it first."
)

// checkCaptainCandidate answers 400/409 and false when p may not become
// the Captain: a remote agent never can, a shared one only once its
// shares are gone.
func checkCaptainCandidate(c *tool.Ctx, p entity.AgentPersona) bool {
	if p.Kind != "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": captainBlockRemote})
		return false
	}
	shares, err := globalTeam.ListShares(c.Context(), p.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return false
	}
	if len(shares) > 0 {
		c.JSON(http.StatusConflict, map[string]string{"error": captainBlockShared})
		return false
	}
	return true
}

// apiTeamAgentMakeCaptain handles POST /api/team/agents/{id}/captain:
// moves the caller's Captain role to {id} in one transaction. Already the
// Captain is a no-op. Answers the new Captain and the previous one's id
// ("" when there was none) so the roster can redraw both.
func apiTeamAgentMakeCaptain(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	if p.IsCaptain {
		users := teamProjectUsersFor(c.Context(), []entity.AgentPersona{p})
		c.JSON(http.StatusOK, map[string]any{"agent": teamAgentToItem(p, users, teamLiveNow(), ownerReach(c)), "previous_id": ""})
		return
	}
	if !checkCaptainCandidate(c, p) {
		return
	}
	prev, err := globalTeam.MakeCaptain(c.Context(), p.OwnerUserID, p.ID)
	if err != nil {
		c.JSON(teamAgentSaveStatus(err), map[string]string{"error": err.Error()})
		return
	}
	if p, err = globalTeam.Get(c.Context(), p.ID); err != nil {
		c.JSON(teamAgentSaveStatus(err), map[string]string{"error": err.Error()})
		return
	}
	users := teamProjectUsersFor(c.Context(), []entity.AgentPersona{p})
	c.JSON(http.StatusOK, map[string]any{"agent": teamAgentToItem(p, users, teamLiveNow(), ownerReach(c)), "previous_id": prev.ID})
}
