package agents

import (
	"context"
	"net/http"
	"strings"

	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/pkg/tool"
)

// The composer's effort picker: GET says what the session's agent can pick
// on its current provider + model, POST saves a pick. A pick applies from
// the agent's next turn; a running turn keeps the effort it started with.

type sessionEffortReq struct {
	Effort string `json:"effort"`
}

// sessionEffortAgent loads the session fresh (agents.json, not the
// registry cache, so a pick made a moment ago is seen) and returns its
// active agent entry.
func sessionEffortAgent(c *tool.Ctx) (string, session.AgentEntry, bool) {
	id := c.PathValue("id")
	cached, ok := globalMgr.Registry().Session(id)
	if !ok || !ownsSession(c, cached) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return "", session.AgentEntry{}, false
	}
	sess, err := session.Load(globalLayout, id)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return "", session.AgentEntry{}, false
	}
	name := sess.Meta.ActiveAgent
	if name == "" {
		name = session.PrimaryAgentName(sess)
	}
	for _, a := range sess.Agents {
		if a.Name == name {
			return id, a, true
		}
	}
	c.JSON(http.StatusUnprocessableEntity, map[string]string{"error": "no agent in session"})
	return "", session.AgentEntry{}, false
}

func sessionEffortInfo(ctx context.Context, id string, a session.AgentEntry) provider.EffortInfo {
	inherited := ""
	if provider.EffortInherited != nil {
		if e, ok := provider.EffortInherited(ctx, id); ok {
			inherited = e
		}
	}
	return provider.EffortInfoFor(ctx, a.Provider, a.ModelID, a.Effort, inherited)
}

func getSessionEffort(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id, a, ok := sessionEffortAgent(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, sessionEffortInfo(c.Context(), id, a))
}

func setSessionEffort(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	var req sessionEffortReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "effort required"})
		return
	}
	req.Effort = strings.TrimSpace(req.Effort)
	id, a, ok := sessionEffortAgent(c)
	if !ok {
		return
	}
	info := sessionEffortInfo(c.Context(), id, a)
	if info.Error != "" {
		c.JSON(http.StatusBadGateway, map[string]string{"error": info.Error})
		return
	}
	// Never save a level the model does not offer: a stale pick would be
	// dropped at every spawn, and the user would think it applied.
	if !provider.ValidEffortChoice(info, req.Effort) {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "effort " + req.Effort + " is not offered by the selected model"})
		return
	}
	if err := globalPool.SetEffort(id, a.Name, req.Effort); err != nil {
		c.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	_ = globalMgr.RefreshSession(id)
	a.Effort = req.Effort
	c.JSON(http.StatusOK, sessionEffortInfo(c.Context(), id, a))
}
