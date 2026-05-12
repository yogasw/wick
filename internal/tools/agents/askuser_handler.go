package agents

import (
	"net/http"
	"strings"

	"github.com/yogasw/wick/internal/agents/askuser"
	"github.com/yogasw/wick/pkg/tool"
)

// answerReq is the body for POST /sessions/{id}/answer. Either
// value (preset option click) or text (freeform input) may be set;
// if both are present, value wins.
type answerReq struct {
	ID    string `json:"id"`
	Value string `json:"value,omitempty"`
	Text  string `json:"text,omitempty"`
}

func notReadyAskUser(c *tool.Ctx) bool {
	return globalAskUsers == nil
}

// answerAsk resolves one pending ask_user request.
func answerAsk(c *tool.Ctx) {
	if notReadyAskUser(c) {
		c.JSON(http.StatusServiceUnavailable, map[string]string{"error": "ask_user not enabled"})
		return
	}
	var req answerReq
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	if req.ID == "" {
		c.JSON(http.StatusBadRequest, map[string]string{"error": "id required"})
		return
	}
	if strings.TrimSpace(req.Value) == "" && strings.TrimSpace(req.Text) == "" {
		c.JSON(http.StatusBadRequest, map[string]string{
			"error": "value or text required",
		})
		return
	}
	if !globalAskUsers.Resolve(req.ID, askuser.Answer{Value: req.Value, Text: req.Text}) {
		c.JSON(http.StatusGone, map[string]string{
			"error": "ask id no longer pending (timed out or already resolved)",
		})
		return
	}
	c.JSON(http.StatusOK, map[string]string{"status": "resolved"})
}

// asksSnapshot lists in-flight asks for the session — used by the
// UI to rehydrate after a reload so a question that arrived while
// the tab was closed still shows up.
func asksSnapshot(c *tool.Ctx) {
	if notReadyAskUser(c) {
		c.JSON(http.StatusOK, map[string]any{"pending": []any{}})
		return
	}
	sid := c.PathValue("id")
	c.JSON(http.StatusOK, map[string]any{
		"pending": globalAskUsers.PendingFor(sid),
	})
}
