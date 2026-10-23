package agents

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

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
			Mode         string `json:"mode"`
		}
		if err := json.NewDecoder(io.LimitReader(c.R.Body, 1<<16)).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid JSON: " + err.Error()})
			return
		}
		if body.Action == "mode" {
			sessionReplyMode(c, id, body.Mode)
			return
		}
		if body.Action != "on" && body.Action != "off" {
			c.JSON(http.StatusBadRequest, map[string]string{"error": "action must be on, off or mode"})
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

// sessionReplyMode is POST /api/sessions/{id}/listen {action: "mode"}: the
// composer's Auto-reply / Listen / Manual switch. Manual stops only this
// thread. Auto-reply and Listen switch the thread on and, when its channel
// behaves otherwise, change the channel's row of the reply rules: a thread
// has no mode of its own to override. The rules are stored where the
// settings page stores them, by the same people: an agent's own bot by
// its owner, a Channels page bot by the user it belongs to.
func sessionReplyMode(c *tool.Ctx, sessionID, mode string) {
	ch := slackChannelOf(sessionID)
	if ch == nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": "this session is not a Slack channel thread"})
		return
	}
	rules, changed, err := ch.ReplyModeRules(sessionID, mode)
	if err != nil {
		c.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var sync func()
	if changed {
		var status int
		sync, status, err = storeReplyRules(c, globalChannels.InstanceKeyOf(ch), rules)
		if err != nil {
			c.JSON(status, map[string]string{"error": err.Error()})
			return
		}
	}
	out, err := ch.ApplyReplyMode(sessionID, mode, rules, changed)
	if err != nil {
		c.JSON(http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if sync != nil {
		sync()
	}
	c.JSON(http.StatusOK, out)
}

// slackChannelOf is the Slack instance that owns sessionID, or nil.
func slackChannelOf(sessionID string) *agentslack.Channel {
	if globalChannels == nil {
		return nil
	}
	for _, c := range globalChannels.Channels() {
		if sc, ok := c.(*agentslack.Channel); ok && sc.OwnsSession(sessionID) {
			return sc
		}
	}
	return nil
}

// errReplyRulesNotYours: the caller may chat in the thread but the bot's
// settings belong to someone else.
var errReplyRulesNotYours = errors.New("only the owner of this Slack bot can change its channel rules")

// storeReplyRules saves rules as channel_reply_rules of the bot behind
// instance key iKey, refusing a caller who could not save it on the
// settings page. sync reloads the bot from the stored row.
func storeReplyRules(c *tool.Ctx, iKey, rules string) (sync func(), status int, err error) {
	if globalDB == nil {
		return nil, http.StatusServiceUnavailable, errors.New("db not ready")
	}
	kv := map[string]string{"channel_reply_rules": rules}
	if agentID, ok := strings.CutPrefix(iKey, agentchannels.AgentSlackType+":"); ok {
		if globalTeam == nil {
			return nil, http.StatusServiceUnavailable, errors.New("team not ready")
		}
		p, err := globalTeam.Get(c.Context(), agentID)
		if err != nil || p.OwnerUserID != actorID(c) {
			return nil, http.StatusForbidden, errReplyRulesNotYours
		}
		if err := agentchannels.UpdateAgentSlackKeys(globalDB, agentID, kv); err != nil {
			return nil, http.StatusInternalServerError, err
		}
		return func() { syncAgentSlack(context.Background(), p) }, 0, nil
	}
	userID := currentUserIDForChannel(c)
	own := "slack:" + userID
	if userID == "" {
		own = "slack:__owner__"
	}
	if iKey != own {
		return nil, http.StatusForbidden, errReplyRulesNotYours
	}
	if err := agentchannels.SetChannelConfigKeysForUser(globalDB, "slack", userID, kv); err != nil {
		return nil, http.StatusInternalServerError, err
	}
	return func() { syncChannelInstance(context.Background(), "slack", userID) }, 0, nil
}
