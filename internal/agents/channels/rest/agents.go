// Package rest — Team agents over the OpenAI-compatible endpoint.
//
// `"model": "agent:<handle>"` runs the turn as that Team agent: its
// persona, access and tools, and for a remote agent its adapter. The
// agent must belong to the token's owner and have its REST connection
// switched on (one agent_channels row per agent, default off). The
// connection is its own gate, so it answers even when the owner's plain
// REST channel is off.

package rest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/yogasw/wick/internal/entity"
)

// AgentModelPrefix marks a model id naming a Team agent.
const AgentModelPrefix = "agent:"

// AgentRowType is the agent_channels.type of an agent's REST connection.
// One row per agent, named after the agent id, keyed "rest-agent:<id>".
const AgentRowType = "rest-agent"

// AgentModelID is the model id that reaches the agent with handle.
func AgentModelID(handle string) string { return AgentModelPrefix + handle }

// IsAgentModel reports whether id names a Team agent.
func IsAgentModel(id string) bool { return strings.HasPrefix(strings.TrimSpace(id), AgentModelPrefix) }

// Agent is a Team agent as the REST channel sees it.
type Agent struct {
	ID          string
	OwnerUserID string
	Handle      string
	ProjectID   string
	Disabled    bool
	// RESTEnabled is the agent's REST connection toggle.
	RESTEnabled bool
}

// AgentDirectory resolves Team agents and their sessions. Implemented by
// the Team tool, which owns both.
type AgentDirectory interface {
	// AgentByHandle returns ownerUserID's agent named handle; ok=false
	// when the owner has none.
	AgentByHandle(ctx context.Context, ownerUserID, handle string) (Agent, bool)
	// Agents lists ownerUserID's agents.
	Agents(ctx context.Context, ownerUserID string) []Agent
	// EnsureSession creates sessionID as a session of a when it does not
	// exist yet. created reports whether it did.
	EnsureSession(ctx context.Context, a Agent, sessionID string) (created bool, err error)
}

// agentDir is set once by the server; nil = no Team, `agent:` ids are
// unknown models.
var agentDir AgentDirectory

// SetAgentDirectory wires the Team side. Called once at boot.
func SetAgentDirectory(d AgentDirectory) { agentDir = d }

// AgentsWired reports whether `agent:` model ids can be served.
func AgentsWired() bool { return agentDir != nil }

// agentModels lists the `agent:<handle>` ids userID may call: their own
// enabled agents whose REST connection is on.
func agentModels(ctx context.Context, userID string) []modelObject {
	if agentDir == nil || userID == "" {
		return nil
	}
	now := time.Now().Unix()
	var out []modelObject
	for _, a := range agentDir.Agents(ctx, userID) {
		if a.Disabled || !a.RESTEnabled || a.OwnerUserID != userID {
			continue
		}
		out = append(out, modelObject{ID: AgentModelID(a.Handle), Object: "model", Created: now, OwnedBy: "agent"})
	}
	return out
}

// resolveAgentModel finds the agent model names for userID. A missing,
// foreign or disabled agent is model_not_found (404) — the caller learns
// nothing about agents that are not theirs; a known agent whose REST
// connection is off is 403.
func resolveAgentModel(ctx context.Context, userID, model string) (Agent, int) {
	handle := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(model), AgentModelPrefix))
	if agentDir == nil || handle == "" || userID == "" {
		return Agent{}, http.StatusNotFound
	}
	a, ok := agentDir.AgentByHandle(ctx, userID, handle)
	if !ok || a.OwnerUserID != userID || a.Disabled {
		return Agent{}, http.StatusNotFound
	}
	if !a.RESTEnabled {
		return Agent{}, http.StatusForbidden
	}
	return a, 0
}

// writeAgentConnectionOff is the OpenAI-shaped 403 for an agent whose REST
// connection is switched off.
func writeAgentConnectionOff(w http.ResponseWriter, model string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"message": "The REST connection of `" + model + "` is off. Switch it on in the agent's Connections.",
			"type":    "permission_error",
			"param":   "model",
			"code":    "rest_connection_disabled",
		},
	})
}

// serveAgentChat answers a chat completion addressed to a Team agent. The
// session is created as the agent's, so the turn runs with its persona
// and access; the history modes and background flag work as on the
// plain endpoint.
func (c *Channel) serveAgentChat(w http.ResponseWriter, r *http.Request, req chatRequest, cl caller) {
	a, status := resolveAgentModel(r.Context(), cl.UserID, req.Model)
	switch status {
	case http.StatusNotFound:
		writeModelNotFound(w, req.Model)
		return
	case http.StatusForbidden:
		writeAgentConnectionOff(w, req.Model)
		return
	}

	conv := resolveConversation(req.Conversation, req.Metadata)
	var prompt, sessionID string
	if conv == "" {
		prompt = flattenMessages(req.Messages)
		sessionID = "rest-" + uuid.NewString()
	} else {
		prompt = lastUserMessage(req.Messages)
		// The agent is part of the key: one conversation string used with
		// two agents is two sessions, each bound to its own agent.
		sessionID = restSessionID(cl.UserID, "agent-"+a.ID+"-"+conv)
	}
	if strings.TrimSpace(prompt) == "" {
		writeError(w, http.StatusBadRequest, "no user message found")
		return
	}

	created, err := agentDir.EnsureSession(context.Background(), a, sessionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create agent session: "+err.Error())
		return
	}
	if created {
		c.injectAgentContext(a, sessionID, cl, req.User)
	}

	// The session exists now, so dispatch's own context inject is skipped
	// (reused=true); the agent's project rides as the override.
	if resolveBackground(req.Background, req.Metadata) {
		if status, msg := c.dispatchBackground(sessionID, cl, req.User, prompt, true, a.ProjectID); status != 0 {
			writeError(w, status, msg)
			return
		}
		writeChatCompletion(w, sessionID, req.Model, "", "queued")
		return
	}
	res, status, msg := c.dispatch(r.Context(), sessionID, cl, req.User, prompt, true, a.ProjectID)
	if status != 0 {
		writeError(w, status, msg)
		return
	}
	if res.errMsg != "" {
		s := http.StatusInternalServerError
		if res.blocked {
			s = http.StatusForbidden
		}
		writeError(w, s, res.errMsg)
		return
	}
	writeChatCompletion(w, sessionID, req.Model, res.text, "")
}

// injectAgentContext sends the one-time origin note of a new agent
// session. Best-effort, like the plain endpoint's.
func (c *Channel) injectAgentContext(a Agent, sessionID string, cl caller, userField string) {
	label := cl.UserID
	if u := strings.TrimSpace(userField); u != "" {
		label = cl.UserID + " (" + u + ")"
	}
	note := fmt.Sprintf(
		"[REST request context — sent automatically by wick]\nUser: %s\nAgent: @%s\nSession: %s",
		label, a.Handle, sessionID,
	)
	if err := c.sendFn(c.newSendCtx(a.ProjectID, cl), sessionID, agentName, "rest", "system", note); err != nil {
		return
	}
	if hook := c.onSessionStart; hook != nil {
		hook(sessionID, "rest", note)
	}
}

// writeChatCompletion writes a chat.completion with one assistant choice.
func writeChatCompletion(w http.ResponseWriter, sessionID, model, text, status string) {
	resp := chatResponse{
		ID:      "wick-" + sessionID + "-" + fmt.Sprintf("%d", time.Now().UnixNano()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   firstNonEmpty(model, "wick"),
		Status:  status,
		Choices: []chatChoice{{
			Index:        0,
			Message:      chatMessage{Role: "assistant", Content: text},
			FinishReason: "stop",
		}},
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// AgentConnStore keeps agents' REST connection toggles on agent_channels.
type AgentConnStore struct{ db *gorm.DB }

// NewAgentConnStore wraps db.
func NewAgentConnStore(db *gorm.DB) *AgentConnStore { return &AgentConnStore{db: db} }

// AgentRowID is the agent_channels primary key of agentID's connection.
func AgentRowID(agentID string) string { return AgentRowType + ":" + agentID }

// Enabled reports whether agentID's REST connection is on. No row = off.
func (s *AgentConnStore) Enabled(agentID string) (bool, error) {
	var rows []entity.AgentChannel
	if err := s.db.Where("type = ? AND name = ?", AgentRowType, agentID).Limit(1).Find(&rows).Error; err != nil {
		return false, err
	}
	return len(rows) > 0 && rows[0].Enabled, nil
}

// EnabledAgents is the set of ownerUserID's agent ids whose connection is on.
func (s *AgentConnStore) EnabledAgents(ownerUserID string) (map[string]bool, error) {
	var rows []entity.AgentChannel
	if err := s.db.Where("type = ? AND user_id = ? AND enabled = ?", AgentRowType, ownerUserID, true).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		out[r.Name] = true
	}
	return out, nil
}

// SetEnabled upserts agentID's toggle.
func (s *AgentConnStore) SetEnabled(agentID, ownerUserID string, enabled bool) error {
	now := time.Now()
	res := s.db.Model(&entity.AgentChannel{}).Where("type = ? AND name = ?", AgentRowType, agentID).
		Updates(map[string]any{"enabled": enabled, "updated_at": now})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		return nil
	}
	owner := ownerUserID
	if err := s.db.Create(&entity.AgentChannel{
		ID: AgentRowID(agentID), Type: AgentRowType, Name: agentID, UserID: &owner,
		Enabled: enabled, Config: "{}", CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil || enabled {
		return err
	}
	// The column defaults to true and Create drops a false bool as a zero
	// value, so an off row is written off explicitly.
	return s.db.Model(&entity.AgentChannel{}).Where("type = ? AND name = ?", AgentRowType, agentID).
		Update("enabled", false).Error
}

// Delete drops agentID's connection, if any.
func (s *AgentConnStore) Delete(agentID string) error {
	return s.db.Where("type = ? AND name = ?", AgentRowType, agentID).Delete(&entity.AgentChannel{}).Error
}
