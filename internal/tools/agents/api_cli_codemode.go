package agents

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/codemode"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/internal/login"
	"github.com/yogasw/wick/internal/mcp"
	mcphandlers "github.com/yogasw/wick/internal/mcp/handlers"
	"github.com/yogasw/wick/pkg/tool"
)

// cliRunCode runs a script as the token's session: the same path as
// wick_run_code (RunCodeInSession → RunCodeAs), narrowed by the agent scope
// of that session. A var so tests can stand in for the runner.
var cliRunCode = func(ctx context.Context, run mcphandlers.SessionCodeRun, sessionID string, user *entity.User, tagIDs []string) (*codemode.Result, error) {
	if globalConnectors == nil {
		return nil, errors.New("connectors not initialised — check server boot logs")
	}
	ctx = mcp.WithAgentScope(ctx, sessionID)
	return mcphandlers.RunCodeInSession(ctx, globalConnectors, globalLayout, run, sessionID, user, tagIDs)
}

// cliSessionAlive reports whether the token's session still exists. A var
// so tests need no manager.
var cliSessionAlive = func(sessionID string) bool {
	if globalMgr == nil {
		return false
	}
	_, found := globalMgr.Registry().Session(sessionID)
	return found
}

// apiCLICodemodeRun runs a Code Mode script from a shell, as the session
// that minted the token — `support-tools codemode run`.
//
// Nothing here picks a session or a person: both come from the token, so a
// token for session A runs as session A and its minter, with A's project
// folder for script_path and A's session connectors. The script's own
// failure is a 200 with ok=false, like wick_run_code's result; only a run
// that never started is an HTTP error.
func apiCLICodemodeRun(c *tool.Ctx) {
	g, ok := cliGrant(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, map[string]string{"error": "no token"})
		return
	}
	user := login.GetUser(c.Context())
	if user == nil || !user.Approved {
		c.JSON(http.StatusUnauthorized, map[string]string{"error": "the person who minted this token is unknown or not approved"})
		return
	}
	if !cliSessionAlive(g.SessionID) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session no longer exists"})
		return
	}
	var req struct {
		Lang       string          `json:"lang"`
		Code       string          `json:"code"`
		ScriptPath string          `json:"script_path"`
		Input      json.RawMessage `json:"input"`
		TimeoutMS  int64           `json:"timeout_ms"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if strings.TrimSpace(string(req.Input)) == "null" {
		req.Input = nil
	}
	run := mcphandlers.SessionCodeRun{
		Lang:       req.Lang,
		Code:       req.Code,
		ScriptPath: req.ScriptPath,
		Input:      req.Input,
		Timeout:    time.Duration(req.TimeoutMS) * time.Millisecond,
	}
	res, err := cliRunCode(c.Context(), run, g.SessionID, user, login.GetUserTagIDs(c.Context()))
	switch {
	case errors.Is(err, mcphandlers.ErrCodeSource):
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	case err != nil:
		log.Ctx(c.Context()).Error().Msgf("cli codemode run %s: %s", g.SessionID, err.Error())
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, res)
}
