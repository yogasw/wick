package wick

import (
	"strings"

	"github.com/yogasw/wick/internal/agents/provider/sessionoverride"
	"github.com/yogasw/wick/pkg/entity"
)

// session_override.go declares wick's per-session runtime overrides — the
// settings the composer's `/`-menu popover edits without sending a message
// (currently just reasoning: the /thinking toggle). The struct's wick tags are
// the SINGLE source of truth: entity.StructToConfigs turns them into the FE
// field spec, entity.MapToStruct reads the saved values back here. Adding a new
// per-session knob = adding a tagged field below; the generic popover renders
// it automatically, no FE change.

// wickSessionOverride is the set of wick settings a user can flip per-session at
// runtime. Registered under the "wick" provider type in the shared
// sessionoverride registry.
type wickSessionOverride struct {
	// ThinkingOn toggles extended reasoning for this session. A Go bool field
	// auto-derives the "checkbox" widget (no explicit flag needed).
	ThinkingOn bool `wick:"key=thinking_on;group=Thinking|Reasoning for this session only.;desc=Let the model think before answering."`
	// ReasoningEffort picks how hard it thinks; only meaningful while on.
	ReasoningEffort string `wick:"key=reasoning_effort;dropdown=low|medium|high;default=medium;visible_when=thinking_on:true;group=Thinking;desc=Reasoning effort."`
}

func init() {
	sessionoverride.Register("wick", wickSessionOverride{})
}

// overrideConfig reads a session's saved overrides into the typed struct.
func overrideConfig(sessionID string) wickSessionOverride {
	var o wickSessionOverride
	sessionoverride.Apply(sessionID, &o)
	return o
}

// overrideReasoning resolves the reasoning request from a session's saved
// overrides, or nil when none is saved (caller falls back to the baseline).
// on=false (explicitly turned off) yields a non-nil "off" marker via the bool
// return so the caller can distinguish it from "unset".
func overrideReasoning(sessionID string) (r *ReasoningConfig, set bool) {
	vals := sessionoverride.Values(sessionID)
	if _, ok := vals["thinking_on"]; !ok {
		return nil, false // no override saved → caller uses the baseline
	}
	if strings.TrimSpace(strings.ToLower(vals["thinking_on"])) != "true" {
		return nil, true // explicitly OFF
	}
	// Read the effort from the RAW value map, not the tag-defaulted struct:
	// MapToStruct would fill an unset reasoning_effort with its `default=`,
	// which would silently override the configured baseline effort. Only an
	// effort the user actually chose counts here; an absent one leaves nil so
	// the caller falls back to its baseline/default (reasoningForOn).
	effort := strings.ToLower(strings.TrimSpace(vals["reasoning_effort"]))
	if effort == "" {
		return nil, true // ON but no explicit effort → caller uses baseline/default
	}
	return &ReasoningConfig{Effort: effort}, true
}

// setThinkingValues writes a thinking on/off (+ optional effort) to the session
// override store — the same store the popover writes through, so the /thinking
// text command and the popover stay one source of truth.
func setThinkingValues(sessionID string, on bool, effort string) {
	sessionoverride.Set(sessionID, "thinking_on", boolStr(on))
	if on && effort != "" {
		sessionoverride.Set(sessionID, "reasoning_effort", effort)
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// SessionOverrideSchema returns the field spec for a provider type (for the
// endpoint). Re-exported here so the tools/agents handler depends on wick, not
// the registry package directly (keeps the provider wiring in one place).
func SessionOverrideSchema(providerType string) []entity.Config {
	return sessionoverride.Schema(providerType)
}

// SessionOverrideValues / SetSessionOverride / ClearSessionOverride wrap the
// registry store for the handler. Values are persisted per-session
// (overrides.json), so Clear is an explicit reset — NOT a teardown step.
func SessionOverrideValues(sessionID string) map[string]string {
	return sessionoverride.Values(sessionID)
}
func SetSessionOverride(sessionID, key, value string) {
	sessionoverride.Set(sessionID, key, value)
}
func ClearSessionOverride(sessionID string) { sessionoverride.Clear(sessionID) }
