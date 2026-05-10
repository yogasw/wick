package session

import (
	"fmt"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/storage"
)

// AgentEntry is one row in sessions/<id>/agents.json. CLISessionID is
// the resume key — wick captures it from the first stream-json event
// emitted by the CLI and persists it so subsequent spawns can pass
// `--resume <id>`. See agents-design.md §5.2.
type AgentEntry struct {
	Name         string    `json:"name"`
	Provider     string    `json:"provider"`
	CLISessionID string    `json:"cli_session_id,omitempty"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	LastActive   time.Time `json:"last_active,omitempty"`
}

// SaveAgents atomically rewrites sessions/<id>/agents.json. nil
// becomes an empty array on disk so consumers don't have to handle
// `null`.
func SaveAgents(layout config.Layout, id string, agents []AgentEntry) error {
	if err := storage.ValidateSessionID(id); err != nil {
		return err
	}
	if !storage.PathExists(layout.SessionDir(id)) {
		return fmt.Errorf("session %q not found", id)
	}
	if agents == nil {
		agents = []AgentEntry{}
	}
	return storage.WriteJSON(layout.SessionAgents(id), agents)
}

// AddAgent appends a new agent entry. Errors on duplicate name within
// the same session.
func AddAgent(layout config.Layout, id, name, provider string) error {
	sess, err := Load(layout, id)
	if err != nil {
		return err
	}
	for _, a := range sess.Agents {
		if a.Name == name {
			return fmt.Errorf("agent %q already exists in session %q", name, id)
		}
	}
	sess.Agents = append(sess.Agents, AgentEntry{
		Name:      name,
		Provider:  provider,
		Status:    "idle",
		CreatedAt: time.Now().UTC(),
	})
	return SaveAgents(layout, id, sess.Agents)
}

// SetActiveAgent updates meta.json's active_agent field. The named
// agent must already exist in agents.json.
func SetActiveAgent(layout config.Layout, id, name string) error {
	sess, err := Load(layout, id)
	if err != nil {
		return err
	}
	found := false
	for _, a := range sess.Agents {
		if a.Name == name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("agent %q not found in session %q", name, id)
	}
	sess.Meta.ActiveAgent = name
	sess.Meta.LastActive = time.Now().UTC()
	return SaveMeta(layout, id, sess.Meta)
}
