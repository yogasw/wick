package provider

import (
	"context"
	"encoding/json"
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
	// tag may be a bare type ("codex") or a named instance ("codex/gemini_flash").
	// Bare type resolves to the per-type default instance (Name == type).
	wantType, wantName := tag, tag
	if i := strings.IndexByte(tag, '/'); i >= 0 {
		wantType, wantName = tag[:i], tag[i+1:]
	}
	found := false
	for _, ins := range instances {
		if string(ins.Type) == wantType && ins.Name == wantName && !ins.Disabled {
			found = true
			break
		}
	}
	if !found {
		names := make([]string, 0, len(instances))
		for _, ins := range instances {
			if ins.Disabled {
				continue
			}
			// Default instance (Name == type) shows as "#codex"; a named
			// instance shows as "#codex/gemini_flash".
			label := string(ins.Type)
			if ins.Name != string(ins.Type) {
				label += "/" + ins.Name
			}
			names = append(names, "#"+label)
		}
		return fmt.Errorf("unknown provider %q — available: %s", tag, strings.Join(names, ", "))
	}

	// 1. Persist new provider to agents.json.
	loaded, err := session.Load(layout, sessionID)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}
	newKey := wantType + "/" + wantName
	// targetHasHistory reports whether the target provider already ran in this
	// session (has a stored resume id), so the notice can say whether it will
	// pick up its own past turns or start cold.
	targetHasHistory := false
	fromKey := "" // provider active before this switch — recorded in the turn extras
	for i, a := range loaded.Agents {
		if a.Name == agentName {
			fromKey = normalizeProviderKey(a.Provider)
			// Reject a no-op switch to the provider already active.
			if normalizeProviderKey(a.Provider) == newKey {
				short := wantType
				if wantName != wantType {
					short = newKey
				}
				return fmt.Errorf("already using %s", short)
			}
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
			targetHasHistory = resumeID != ""
			loaded.Agents[i].CLISessionID = resumeID
			break
		}
	}
	if err := session.SaveAgents(layout, sessionID, loaded.Agents); err != nil {
		return fmt.Errorf("save agents: %w", err)
	}

	// Collapse a run of back-to-back switches: if the tail of the history is
	// nothing but switch artifacts (no real chat since the last switch), drop
	// them so only this switch remains. Keeps the transcript clean when the
	// user flips providers a few times before actually sending a message.
	convPath := layout.SessionConversation(sessionID)
	pruneTrailingSwitchTurns(convPath, sessionID)

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

	// Each provider keeps its own conversation context — a CLI only sees the
	// turns from its own runs. Tell the user the new provider won't read the
	// turns produced by other providers, unless it has run here before (then
	// --resume restores its own history).
	contextNote := "Note: " + tag + " won't see earlier turns from other providers in this session — each provider keeps its own context."
	if targetHasHistory {
		contextNote = "Note: resuming " + tag + "'s own earlier turns; it still won't see turns from other providers."
	}

	steps := []string{
		"Saved provider to agents.json",
		"Killing running agent process",
		"Next message spawns with provider: " + tag,
		contextNote,
	}

	// 3. Record ONE structured system turn — no separate assistant bubble.
	// kind + extras let the UI render it as a switch card and let future
	// features attach their own data to a system turn without a schema change.
	events := make([]store.TurnEvent, 0, len(steps))
	for _, s := range steps {
		events = append(events, store.TurnEvent{Type: store.KindProviderSwitch, Text: s, At: now})
	}
	_ = storage.AppendJSONL(
		layout.SessionConversation(sessionID),
		"wick-conv-v1",
		sessionID,
		store.ConversationTurn{
			Timestamp: now,
			Role:      "system",
			Source:    source,
			Kind:      store.KindProviderSwitch,
			Text:      "Provider switched → " + tag,
			Extras: map[string]string{
				"from": fromKey,
				"to":   newKey,
				"note": contextNote,
			},
			Events: events,
		},
	)

	// 4. Notify caller (e.g. push SSE) so the UI appends the system turn live.
	if opts.Notify != nil {
		opts.Notify(tag, steps)
	}

	// 5. Reply is for channels with no system-turn concept (Slack, Telegram):
	// they get a plain text confirmation. The web UI passes no Reply — its
	// system turn already carries everything, so no assistant bubble is made.
	replyText := "Switched to " + tag + ". Your next message will use " + tag + ".\n\n" + contextNote
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

// isSwitchArtifact reports whether a turn was written by a previous Switch
// call — the system trace turn (carries provider_switch events) or its paired
// assistant confirmation ("Switched to …"). User turns are never treated as
// artifacts: a "#tag" the user typed is real intent worth keeping, and this
// avoids ever eating a genuine one-word message.
func isSwitchArtifact(t store.ConversationTurn) bool {
	switch t.Role {
	case "system":
		for _, e := range t.Events {
			if e.Type == "provider_switch" {
				return true
			}
		}
	case "assistant":
		return strings.HasPrefix(t.Text, "Switched to ")
	}
	return false
}

// pruneTrailingSwitchTurns rewrites conversation.jsonl without the run of
// switch-artifact turns at its tail. No-op (and best-effort) when there are
// none or on any read/write error — a failed prune must never block a switch.
func pruneTrailingSwitchTurns(convPath, sessionID string) {
	var turns []store.ConversationTurn
	if err := storage.ReadJSONL(convPath, func(line []byte) bool {
		var t store.ConversationTurn
		if err := json.Unmarshal(line, &t); err == nil {
			turns = append(turns, t)
		}
		return true
	}); err != nil || len(turns) == 0 {
		return
	}

	keep := len(turns)
	for keep > 0 && isSwitchArtifact(turns[keep-1]) {
		keep--
	}
	if keep == len(turns) {
		return // nothing trailing to drop
	}

	if err := storage.TruncateJSONL(convPath, "wick-conv-v1", sessionID); err != nil {
		return
	}
	for _, t := range turns[:keep] {
		_ = storage.AppendJSONL(convPath, "wick-conv-v1", sessionID, t)
	}
}
