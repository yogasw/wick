package agents

import (
	"errors"
	"net/http"

	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/pkg/tool"
)

// apiTeamAgentRBAC handles GET /api/team/agents/{id}/rbac: the role
// document in force (the default one when none is saved), its revision,
// whether the agent is in By role access, and the latest revisions.
// Owner only.
func apiTeamAgentRBAC(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	doc, rev, err := globalTeam.RBAC(c.Context(), p.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	hist, err := globalTeam.RBACHistory(c.Context(), p.ID, 10)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{
		"enabled": team.NormalizeAccessMode(p.AccessMode) == team.AccessRoles,
		"rev":     rev,
		"doc":     doc,
		"history": hist,
	})
}

// apiTeamRoleTags handles GET /api/team/role-tags: the wick tags a tag
// rule can name, for the roles editor.
func apiTeamRoleTags(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	tags, err := globalTeam.RoleTags(c.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": "could not load tags"})
		return
	}
	if tags == nil {
		tags = []team.RoleTag{}
	}
	c.JSON(http.StatusOK, map[string]any{"tags": tags})
}

// rbacWriteReq is PUT /rbac's body: the whole document, the revision it
// was edited from, and why.
type rbacWriteReq struct {
	Doc     team.RBACDoc `json:"doc"`
	BaseRev int          `json:"base_rev"`
	Reason  string       `json:"reason"`
}

// apiTeamAgentRBACPut handles PUT /api/team/agents/{id}/rbac: saves the
// document as a new revision. A document edited from an older revision
// than the one in force answers 409 instead of overwriting it. Owner
// only.
func apiTeamAgentRBACPut(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	var req rbacWriteReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
		return
	}
	rev, err := globalTeam.SetRBAC(c.Context(), p.ID, req.Doc, req.BaseRev, actorID(c), req.Reason)
	switch {
	case errors.Is(err, team.ErrRBACConflict):
		c.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
		return
	case err != nil:
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	globalTeam.Invalidate(p.ID)
	c.JSON(http.StatusOK, map[string]any{"rev": rev})
}
