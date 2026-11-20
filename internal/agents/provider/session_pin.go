package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/session"
)

// SessionDefault, when set, names the provider ("type/name") and model a
// session goes back to when its pin is cleared: its Team agent's default,
// else its project's. ok false = there is none, and a cleared session
// stays where it is. The agents tool wires it, as it does AgentDefault.
var SessionDefault func(ctx context.Context, sessionID string) (tag, modelID string, ok bool)

// PinResult is what PinSession / UnpinSession left on the session.
type PinResult struct {
	Agent    string `json:"agent"`
	Provider string `json:"provider"`
	Model    string `json:"model,omitempty"`
	Pinned   bool   `json:"pinned"`
	// Switched is true when the provider or model changed. The running
	// process (if any) is left alone: the pool respawns it on the new
	// pair at the session's next message, so no turn is cut short.
	Switched bool `json:"switched"`
}

// PinSession puts sessionID's agent on tag + modelID and pins it there,
// so the agent's default (locked or not) no longer moves it. agentName ""
// = the session's primary agent. A session with no agent entry yet (a
// workflow's session_init before the first send) gets one. The access
// check is the failover guard's (the agent owner's providers): a pin is
// the owner's plan for the session, not a person picking a provider, so
// the agent's "keeps its provider" setting does not refuse it.
func PinSession(ctx context.Context, layout config.Layout, sessionID, agentName, tag, modelID string, opts SwitchOptions) (PinResult, error) {
	tag, modelID = strings.TrimSpace(tag), strings.TrimSpace(modelID)
	if tag == "" {
		return PinResult{}, errors.New("provider is required")
	}
	key := normalizeProviderKey(tag)
	if err := CheckFailover(ctx, sessionID, key); err != nil {
		return PinResult{}, err
	}
	sess, err := session.Load(layout, sessionID)
	if err != nil {
		return PinResult{}, err
	}
	if agentName == "" {
		agentName = session.PrimaryAgentName(sess)
	}
	var cur *session.AgentEntry
	for i := range sess.Agents {
		if sess.Agents[i].Name == agentName {
			cur = &sess.Agents[i]
			break
		}
	}
	res := PinResult{Agent: agentName, Provider: key, Model: modelID, Pinned: true}
	switch {
	case cur == nil || strings.TrimSpace(cur.Provider) == "":
		// Nothing ran yet: write the entry directly, no switch notice.
		if err := knownInstance(key); err != nil {
			return PinResult{}, err
		}
		err = session.UpdateAgents(layout, sessionID, func(agents []session.AgentEntry) ([]session.AgentEntry, bool, error) {
			for i := range agents {
				if agents[i].Name == agentName {
					setPinnedEntry(&agents[i], key, modelID)
					return agents, true, nil
				}
			}
			a := session.AgentEntry{Name: agentName, Status: "idle", CreatedAt: time.Now().UTC()}
			setPinnedEntry(&a, key, modelID)
			return append(agents, a), true, nil
		})
		if err != nil {
			return PinResult{}, fmt.Errorf("save agents: %w", err)
		}
		res.Switched = true
	case normalizeProviderKey(cur.Provider) == key && cur.ModelID == modelID:
		if err := session.SetPinned(layout, sessionID, agentName, true); err != nil {
			return PinResult{}, err
		}
	default:
		pin := true
		opts.ModelID, opts.Pin = modelID, &pin
		if opts.Source == "" {
			opts.Source = "agent"
		}
		if _, err := ApplySwitch(layout, sessionID, agentName, key, opts); err != nil {
			return PinResult{}, err
		}
		res.Switched = true
	}
	return res, nil
}

// UnpinSession clears sessionID's pin and puts the agent back on
// SessionDefault. With no default the session keeps its current pair,
// now unpinned. agentName "" = the session's primary agent.
func UnpinSession(ctx context.Context, layout config.Layout, sessionID, agentName string, opts SwitchOptions) (PinResult, error) {
	sess, err := session.Load(layout, sessionID)
	if err != nil {
		return PinResult{}, err
	}
	if agentName == "" {
		agentName = session.PrimaryAgentName(sess)
	}
	var cur *session.AgentEntry
	for i := range sess.Agents {
		if sess.Agents[i].Name == agentName {
			cur = &sess.Agents[i]
			break
		}
	}
	if cur == nil {
		return PinResult{Agent: agentName}, nil
	}
	res := PinResult{Agent: agentName, Provider: normalizeProviderKey(cur.Provider), Model: cur.ModelID}
	tag, model, ok := "", "", false
	if SessionDefault != nil {
		tag, model, ok = SessionDefault(ctx, sessionID)
	}
	if !ok || tag == "" || strings.TrimSpace(cur.Provider) == "" ||
		(normalizeProviderKey(cur.Provider) == normalizeProviderKey(tag) && cur.ModelID == model) {
		return res, session.SetPinned(layout, sessionID, agentName, false)
	}
	pin := false
	opts.ModelID, opts.Pin = model, &pin
	if opts.Source == "" {
		opts.Source = "agent"
	}
	if _, err := ApplySwitch(layout, sessionID, agentName, normalizeProviderKey(tag), opts); err != nil {
		return PinResult{}, err
	}
	res.Provider, res.Model, res.Switched = normalizeProviderKey(tag), model, true
	return res, nil
}

// setPinnedEntry points a on key + modelID, pinned, with that pair as its
// home (as a manual switch records one).
func setPinnedEntry(a *session.AgentEntry, key, modelID string) {
	a.Provider, a.ModelID, a.Pinned = key, modelID, true
	a.Failover = &session.FailoverState{HomeProvider: key, HomeModelID: modelID}
}

// knownInstance refuses a key naming no enabled provider instance.
func knownInstance(key string) error {
	instances, err := Load()
	if err != nil {
		return fmt.Errorf("load providers: %w", err)
	}
	for _, ins := range instances {
		if !ins.Disabled && string(ins.Type)+"/"+ins.Name == key {
			return nil
		}
	}
	return fmt.Errorf("unknown provider %q", key)
}
