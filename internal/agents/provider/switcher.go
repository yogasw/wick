package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
	"github.com/yogasw/wick/internal/agents/store"
)

// Pool is the subset of pool.Pool that Switch needs.
type Pool interface {
	Kill(sessionID, agentName string) error
	Send(ctx context.Context, sessionID, agentName, source, role, text string) error
}

// SwitchOptions controls optional behaviour after a provider switch.
type SwitchOptions struct {
	// Greeting, if non-empty, is sent as a user message to the new agent
	// immediately after the switch so it can introduce itself.
	Greeting string
	// Source is the transport label written into conversation.jsonl ("ui", "slack", etc.).
	Source string
	// UserText is the original raw message (e.g. "#codex") to record as a
	// user turn in conversation.jsonl before the switch events.
	UserText string
	// Notify, if set, is called after the system turn is written so the
	// caller can push a realtime event (e.g. SSE) to connected clients.
	// tag is the new provider tag; steps are the trace lines.
	Notify func(tag string, steps []string)
	// Reply, if set, is called with the confirmation text so every channel
	// (UI SSE, Slack postReply, REST response, etc.) can deliver it back to
	// the user without forwarding to the provider.
	Reply func(text string)
}

// normalizeProviderKey returns "type/name" form. Bare "type" becomes "type/type".
func normalizeProviderKey(key string) string {
	if strings.Contains(key, "/") {
		return key
	}
	return key + "/" + key
}

// Switch changes the provider for sessionID+agentName, persists the change to
// agents.json, records a system turn in conversation.jsonl (with step trace),
// kills the running agent, and optionally sends a greeting to the new agent.
func Switch(layout config.Layout, pool Pool, sessionID, agentName, tag string, opts SwitchOptions) error {
	instances, err := Load()
	if err != nil {
		return fmt.Errorf("load providers: %w", err)
	}
	found := false
	for _, ins := range instances {
		if string(ins.Type) == tag && !ins.Disabled {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, 0, len(instances))
		for _, ins := range instances {
			if !ins.Disabled {
				names = append(names, "#"+string(ins.Type))
			}
		}
		return fmt.Errorf("unknown provider %q — available: %s", tag, strings.Join(names, ", "))
	}

	// 1. Persist new provider to agents.json.
	loaded, err := session.Load(layout, sessionID)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}
	newKey := tag + "/" + tag
	for i, a := range loaded.Agents {
		if a.Name == agentName {
			if a.CLISessionID != "" {
				if loaded.Agents[i].ProviderSessions == nil {
					loaded.Agents[i].ProviderSessions = map[string]string{}
				}
				// Always store under normalized "type/name" key.
				normalizedCurrent := normalizeProviderKey(a.Provider)
				loaded.Agents[i].ProviderSessions[normalizedCurrent] = a.CLISessionID
			}
			loaded.Agents[i].Provider = newKey
			// Lookup resume ID: prefer exact "type/name", fall back to bare "type".
			resumeID := loaded.Agents[i].ProviderSessions[newKey]
			if resumeID == "" {
				resumeID = loaded.Agents[i].ProviderSessions[tag]
			}
			loaded.Agents[i].CLISessionID = resumeID
			break
		}
	}
	if err := session.SaveAgents(layout, sessionID, loaded.Agents); err != nil {
		return fmt.Errorf("save agents: %w", err)
	}

	// 2. Record user turn (the raw "#provider" message) so it appears in history.
	now := time.Now().UTC()
	source := opts.Source
	if source == "" {
		source = "system"
	}
	if opts.UserText != "" {
		_ = storage.AppendJSONL(
			layout.SessionConversation(sessionID),
			"wick-conv-v1",
			sessionID,
			store.ConversationTurn{
				Timestamp: now,
				Role:      "user",
				Source:    source,
				Text:      opts.UserText,
			},
		)
	}

	// 3. Record system turn in conversation.jsonl.
	_ = storage.AppendJSONL(
		layout.SessionConversation(sessionID),
		"wick-conv-v1",
		sessionID,
		store.ConversationTurn{
			Timestamp: now,
			Role:      "system",
			Source:    source,
			Text:      "Provider switched → " + tag,
			Events: []store.TurnEvent{
				{Type: "provider_switch", Text: "Saved provider to agents.json", At: now},
				{Type: "provider_switch", Text: "Killing running agent process", At: now},
				{Type: "provider_switch", Text: "Next message spawns with provider: " + tag, At: now},
			},
		},
	)

	steps := []string{
		"Saved provider to agents.json",
		"Killing running agent process",
		"Next message spawns with provider: " + tag,
	}

	// 3. Notify caller (e.g. push SSE) if wired up.
	if opts.Notify != nil {
		opts.Notify(tag, steps)
	}

	// 4. Write confirmation as assistant turn + call Reply so the channel
	// (Slack, UI, REST) shows a response without forwarding to the provider.
	replyText := "Switched to " + tag + ". Your next message will use " + tag + "."
	_ = storage.AppendJSONL(
		layout.SessionConversation(sessionID),
		"wick-conv-v1",
		sessionID,
		store.ConversationTurn{
			Timestamp: now,
			Role:      "assistant",
			Agent:     agentName,
			Source:    source,
			Text:      replyText,
		},
	)
	if opts.Reply != nil {
		opts.Reply(replyText)
	}

	// 5. Kill async — file already updated, next Send will spawn with new provider.
	go func() { _ = pool.Kill(sessionID, agentName) }()

	// 6. Optional greeting to warm up the new agent.
	if opts.Greeting != "" {
		go func() {
			_ = pool.Send(context.Background(), sessionID, agentName, source, "user", opts.Greeting)
		}()
	}

	return nil
}
