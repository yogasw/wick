package agents

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/yogasw/wick/internal/agents/team"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// Preview callers for GET /api/team/agents/{id}/access/effective.
const (
	effectiveAsOwner = "owner" // the agent's owner
	effectiveAsUser  = "user"  // one wick user, ?user_id=
	effectiveAsGuest = "guest" // someone with no connectors of their own
)

// effectiveFor is what caller `as` (and userID for effectiveAsUser) gets
// on every connector of p's owner, using the scope p runs under.
func effectiveFor(ctx context.Context, p entity.AgentPersona, as, userID string) ([]team.EffectiveRow, error) {
	cat, err := globalTeam.OwnerCatalog(ctx, p.OwnerUserID)
	if err != nil {
		return nil, err
	}
	scope := globalTeam.ScopeForAgent(ctx, p)
	caller := team.EffectiveCaller{}
	switch as {
	case effectiveAsOwner:
		caller = team.EffectiveCaller{UserID: p.OwnerUserID, Owner: true}
	case effectiveAsUser:
		caller.UserID = userID
		if userID == p.OwnerUserID {
			caller.Owner = true
		} else if globalConnectors != nil && globalAuth != nil && userID != "" {
			if u, err := globalAuth.GetUserByID(ctx, userID); err == nil && u != nil {
				tags := globalAuth.GetUserFilterTagIDs(ctx, u.ID)
				caller.Reaches = func(connectorID string) bool {
					ok, err := globalConnectors.IsVisibleTo(ctx, connectorID, u.ID, tags, u.IsAdmin())
					return err == nil && ok
				}
			}
		}
	}
	var accounts func(string) []entity.ConnectorAccount
	if globalConnectors != nil {
		accounts = func(id string) []entity.ConnectorAccount {
			accs, _ := globalConnectors.ListAccounts(ctx, id)
			return accs
		}
	}
	return team.Effective(scope, cat, caller, accounts), nil
}

// apiTeamAgentAccessEffective handles GET
// /api/team/agents/{id}/access/effective?as=owner|user|guest&user_id=:
// the access a caller gets through the agent, computed by the same scope
// and lend rules a run uses. Read-only; owner only.
func apiTeamAgentAccessEffective(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	as := c.Query("as")
	switch as {
	case effectiveAsOwner, effectiveAsUser, effectiveAsGuest:
	case "":
		as = effectiveAsOwner
	default:
		c.JSON(http.StatusBadRequest, map[string]string{"error": "as must be owner, user or guest"})
		return
	}
	userID := c.Query("user_id")
	if as == effectiveAsUser && userID == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "user_id is required with as=user"})
		return
	}
	rows, err := effectiveFor(c.Context(), p, as, userID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, map[string]any{"as": as, "user_id": userID, "items": rows})
}

// accessTrailItem is one lent run for the agent's trail.
type accessTrailItem struct {
	RunID          string    `json:"run_id"`
	ConnectorID    string    `json:"connector_id"`
	ConnectorLabel string    `json:"connector_label"`
	Operation      string    `json:"operation"`
	Status         string    `json:"status"`
	CallerID       string    `json:"caller_id"`
	CallerName     string    `json:"caller_name"`
	OnBehalfOfID   string    `json:"on_behalf_of_id"`
	OnBehalfOfName string    `json:"on_behalf_of_name"`
	SessionID      string    `json:"session_id"`
	At             time.Time `json:"at"`
}

// accessTrailLimit caps ?limit= on the trail.
const accessTrailLimit = 200

// apiTeamAgentAccessTrail handles GET /api/team/agents/{id}/access/trail:
// the agent's latest lent runs (who asked, on whose behalf, which op),
// newest first. Owner only.
func apiTeamAgentAccessTrail(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	limit := 50
	if n, err := strconv.Atoi(c.Query("limit")); err == nil && n > 0 {
		limit = min(n, accessTrailLimit)
	}
	out := []accessTrailItem{}
	if globalConnectors == nil {
		c.JSON(http.StatusOK, map[string]any{"items": out})
		return
	}
	rows, err := globalConnectors.RecentRunLendsByAgent(c.Context(), p.ID, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	names := map[string]string{}
	name := func(id string) string {
		if id == "" || globalAuth == nil {
			return ""
		}
		if n, ok := names[id]; ok {
			return n
		}
		n := ""
		if u, err := globalAuth.GetUserByID(c.Context(), id); err == nil && u != nil {
			n = u.Name
		}
		names[id] = n
		return n
	}
	labels := map[string]string{}
	label := func(id string) string {
		if l, ok := labels[id]; ok {
			return l
		}
		l := ""
		if row, err := globalConnectors.Get(c.Context(), id); err == nil && row != nil {
			l = row.Label
		}
		labels[id] = l
		return l
	}
	for _, r := range rows {
		out = append(out, accessTrailItem{
			RunID: r.RunID, ConnectorID: r.ConnectorID, ConnectorLabel: label(r.ConnectorID), Operation: r.OperationKey,
			Status: string(r.Status), CallerID: r.CallerUserID, CallerName: name(r.CallerUserID),
			OnBehalfOfID: r.OnBehalfOf, OnBehalfOfName: name(r.OnBehalfOf), SessionID: r.SessionID, At: r.StartedAt,
		})
	}
	c.JSON(http.StatusOK, map[string]any{"items": out})
}
