package agents

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/channels/rest"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// restBasePath is where the OpenAI-compatible routes are mounted; an SDK's
// base URL is this path on the app URL.
const restBasePath = "/integrations/rest/api/v1/openai"

// tokensPagePath is where a user mints the Personal Access Token the REST
// connection authenticates with.
const tokensPagePath = "/profile/tokens"

// RESTDirectory is the Team side of the REST channel's `agent:<handle>`
// models.
func RESTDirectory() rest.AgentDirectory { return restDirectory{} }

type restDirectory struct{}

func restConnStore() *rest.AgentConnStore {
	if globalDB == nil {
		return nil
	}
	return rest.NewAgentConnStore(globalDB)
}

func restAgentOf(p entity.AgentPersona, enabled bool) rest.Agent {
	return rest.Agent{
		ID: p.ID, OwnerUserID: p.OwnerUserID, Handle: p.Handle, ProjectID: p.ProjectID,
		Disabled: p.Disabled, RESTEnabled: enabled,
	}
}

func (restDirectory) AgentByHandle(ctx context.Context, ownerUserID, handle string) (rest.Agent, bool) {
	st := restConnStore()
	if globalTeam == nil || st == nil {
		return rest.Agent{}, false
	}
	p, err := globalTeam.GetByHandle(ctx, ownerUserID, strings.ToLower(handle))
	if err != nil {
		return rest.Agent{}, false
	}
	on, err := st.Enabled(p.ID)
	if err != nil {
		log.Warn().Err(err).Str("agent", p.ID).Msg("team: read rest connection")
	}
	return restAgentOf(p, on), true
}

func (restDirectory) Agents(ctx context.Context, ownerUserID string) []rest.Agent {
	st := restConnStore()
	if globalTeam == nil || st == nil {
		return nil
	}
	on, err := st.EnabledAgents(ownerUserID)
	if err != nil || len(on) == 0 {
		return nil
	}
	rows, err := globalTeam.List(ctx, ownerUserID)
	if err != nil {
		return nil
	}
	out := make([]rest.Agent, 0, len(on))
	for _, p := range rows {
		out = append(out, restAgentOf(p, on[p.ID]))
	}
	return out
}

func (restDirectory) EnsureSession(ctx context.Context, a rest.Agent, sessionID string) (bool, error) {
	return ensureTeamAgentSession(ctx, session.Origin("rest"), a.ID, a.OwnerUserID, a.ProjectID, sessionID)
}

// AgentRESTStatus is GET /api/team/agents/{id}/rest. It never carries a
// token: the connection authenticates with the caller's own Personal
// Access Token, minted on TokensURL.
type AgentRESTStatus struct {
	Enabled   bool   `json:"enabled"`
	BaseURL   string `json:"base_url"`
	Model     string `json:"model"`
	TokensURL string `json:"tokens_url"`
}

func restStatusOf(p entity.AgentPersona, enabled bool) AgentRESTStatus {
	base := ""
	if globalConfigs != nil {
		base = strings.TrimRight(globalConfigs.AppURL(), "/")
	}
	return AgentRESTStatus{
		Enabled: enabled, BaseURL: base + restBasePath,
		Model: rest.AgentModelID(p.Handle), TokensURL: base + tokensPagePath,
	}
}

func restStoreReady(c *tool.Ctx) (*rest.AgentConnStore, bool) {
	st := restConnStore()
	if st == nil {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "database not ready"})
		return nil, false
	}
	return st, true
}

// apiTeamAgentRESTGet handles GET /api/team/agents/{id}/rest.
func apiTeamAgentRESTGet(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	st, ok := restStoreReady(c)
	if !ok {
		return
	}
	on, err := st.Enabled(p.ID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, restStatusOf(p, on))
}

// apiTeamAgentRESTPut handles PUT /api/team/agents/{id}/rest: the toggle,
// autosaved.
func apiTeamAgentRESTPut(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.NewDecoder(io.LimitReader(c.R.Body, 4<<10)).Decode(&body); err != nil || body.Enabled == nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "body must be {\"enabled\": true|false}"})
		return
	}
	st, ok := restStoreReady(c)
	if !ok {
		return
	}
	if err := st.SetEnabled(p.ID, p.OwnerUserID, *body.Enabled); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, restStatusOf(p, *body.Enabled))
}

// RESTTestResult is POST /api/team/agents/{id}/rest/test: whether a call
// with the model id would be routed to the agent, checked in-process so
// the agent is not woken and no token is needed.
type RESTTestResult struct {
	OK     bool   `json:"ok"`
	Model  string `json:"model"`
	Detail string `json:"detail"`
}

// apiTeamAgentRESTTest handles POST /api/team/agents/{id}/rest/test.
func apiTeamAgentRESTTest(c *tool.Ctx) {
	if !teamReady(c) {
		return
	}
	p, ok := loadOwnTeamAgent(c)
	if !ok {
		return
	}
	res := RESTTestResult{Model: rest.AgentModelID(p.Handle)}
	a, found := restDirectory{}.AgentByHandle(c.Context(), p.OwnerUserID, p.Handle)
	switch {
	case !rest.AgentsWired():
		res.Detail = "The REST endpoint is not running on this server."
	case !found:
		res.Detail = "The model id does not resolve to this agent."
	case a.Disabled:
		res.Detail = "The agent is disabled, so the model id answers model_not_found."
	case !a.RESTEnabled:
		res.Detail = "The REST connection is off, so calls are refused with 403."
	default:
		res.OK, res.Detail = true, "Calls with this model id reach the agent. Authenticate with your Personal Access Token."
	}
	c.JSON(http.StatusOK, res)
}

// removeAgentREST drops a deleted agent's REST connection.
func removeAgentREST(agentID string) {
	st := restConnStore()
	if st == nil {
		return
	}
	if err := st.Delete(agentID); err != nil {
		log.Warn().Err(err).Str("agent", agentID).Msg("team: drop rest connection")
	}
}
