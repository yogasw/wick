package agents

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentslack "github.com/yogasw/wick/internal/agents/channels/slack"
	"github.com/yogasw/wick/internal/agents/pool"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/tool"
)

// agentListenInfo is the listen-mode block of an agent's Slack settings:
// which LISTEN.md files shape it and what a new thread gets from them.
type agentListenInfo struct {
	WickPath    string `json:"wick_path"`
	WickExists  bool   `json:"wick_exists"`
	AgentPath   string `json:"agent_path,omitempty"`
	AgentExists bool   `json:"agent_exists"`
	agentslack.ListenEffective
}

func agentListenInfoOf(p entity.AgentPersona) *agentListenInfo {
	if globalLayout.BaseDir == "" {
		return nil
	}
	wickPath := pool.WickListenDocPath(globalLayout)
	agentPath := pool.AgentListenDocPath(globalLayout, p.ProjectID)
	info := &agentListenInfo{
		WickPath:        wickPath,
		WickExists:      fileExists(wickPath),
		AgentPath:       agentPath,
		AgentExists:     agentPath != "" && fileExists(agentPath),
		ListenEffective: agentslack.EffectiveListen(pool.ReadListenDoc(wickPath), pool.ReadListenDoc(agentPath)),
	}
	return info
}

func fileExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// apiSessionListen handles GET and POST /api/sessions/{id}/listen: the
// Slack listen state of the thread a session is bound to, and the header's
// toggle. A session that is no Slack channel thread answers 404, so the
// chip stays hidden.
func apiSessionListen(c *tool.Ctx) {
	if notReady(c) {
		return
	}
	id := c.PathValue("id")
	sess, ok := globalMgr.Registry().Session(id)
	if !ok || !ownsSession(c, sess) {
		c.JSON(http.StatusNotFound, map[string]string{"error": "session not found"})
		return
	}
	if globalChannels == nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "no channels"})
		return
	}
	req := agentchannels.ListenRequest{Action: "status"}
	if c.R.Method == http.MethodPost {
		var body struct {
			Action       string `json:"action"`
			Instructions string `json:"instructions"`
			DelaySec     *int   `json:"delay_sec"`
		}
		if err := json.NewDecoder(io.LimitReader(c.R.Body, 1<<16)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		if body.Action != "on" && body.Action != "off" {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "action must be on or off"})
			return
		}
		req = agentchannels.ListenRequest{Action: body.Action, Instructions: body.Instructions, DelaySec: body.DelaySec}
	}
	out, err := globalChannels.ThreadListen(id, req)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, out)
}
