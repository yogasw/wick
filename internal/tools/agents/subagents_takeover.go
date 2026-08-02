package agents

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/yogasw/wick/internal/agents/delegation"
	"github.com/yogasw/wick/pkg/tool"
)

// takeOverSubAgent handles POST /api/delegations/{delegationID}/message —
// a human sending guidance into a running sub-agent.
//
// Gated per role by AllowTakeOver, and every steered delegation is
// flagged so the leader is told the answer was shaped by a person. The
// point is not to restrict the human but to keep the provenance honest.
func takeOverSubAgent(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	if globalDelegation == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "sub-agents are not enabled"})
		return
	}
	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(c.R.Body, 1<<20)).Decode(&body); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if body.Message == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "message is required"})
		return
	}

	actorID, isAdmin := callerIdentity(c)
	err := globalDelegation.TakeOver(c.Context(), c.PathValue("delegationID"), actorID, body.Message, isAdmin)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, map[string]string{"status": "sent"})
	case errors.Is(err, delegation.ErrTakeOverNotAllowed):
		// 403 with a specific reason: the caller may well be allowed to
		// stop this sub-agent, just not to steer it.
		c.JSON(http.StatusForbidden, map[string]string{
			"error": "this sub-agent role does not allow take-over",
		})
	case errors.Is(err, delegation.ErrNotSteerable):
		c.JSON(http.StatusConflict, map[string]string{"error": "sub-agent is not running"})
	case errors.Is(err, delegation.ErrForbidden):
		c.JSON(http.StatusForbidden, map[string]string{"error": "not allowed"})
	case errors.Is(err, delegation.ErrDelegationNotFound):
		c.JSON(http.StatusNotFound, map[string]string{"error": "sub-agent not found"})
	default:
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}
}
