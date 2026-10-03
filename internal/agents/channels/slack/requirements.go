// Package slack — requirements.go: the one list of what each channel
// feature needs from the Slack app (bot scopes + event subscriptions).
//
// Purpose: the manifest generator, the health check and the shipped
// docs/slack-app-manifest.json all read this table, so a feature that
// gains a scope or an event cannot leave one of them behind. Tests fail
// when the docs manifest lags.
//
// Main Functions:
//   - Requirements()       — the feature table, in display order
//   - RequiredBotScopes()  — union of every feature's bot scopes
//   - RequiredBotEvents()  — union of every feature's events
//   - FeatureActive()      — whether a feature counts for a given config

package slack

import (
	"sort"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
)

// Need says when a feature's requirements count against the health check.
type Need string

const (
	// NeedAlways — every Slack instance needs it.
	NeedAlways Need = "always"
	// NeedWhenOn — only when the config switches the feature on; off
	// renders as "· off", never as a failure.
	NeedWhenOn Need = "when_on"
	// NeedOptional — missing is a warning, never an error.
	NeedOptional Need = "optional"
	// NeedInfo — listed for completeness only.
	NeedInfo Need = "info"
)

// Feature keys. Stable: the health response and the UI key off them.
const (
	FeatureCore          = "core"
	FeatureDM            = "dm"
	FeatureGroupDM       = "group_dm"
	FeatureIdentity      = "identity"
	FeatureGroupAccess   = "group_access"
	FeatureReactionReply = "reaction_reply"
	FeatureAgentView     = "agent_view"
	FeatureFiles         = "files"
	FeatureCanvasList    = "canvas_list"
)

// Feature is one row of the requirement matrix.
type Feature struct {
	Key       string   `json:"key"`
	Label     string   `json:"label"`
	BotScopes []string `json:"bot_scopes"`
	Events    []string `json:"events"`
	Need      Need     `json:"need"`
}

// requirements is §8.3d of the agents plan. Order is display order.
var requirements = []Feature{
	{Key: FeatureCore, Label: "Mentions and replies", Need: NeedAlways,
		BotScopes: []string{"app_mentions:read", "chat:write", "channels:history", "groups:history"},
		Events:    []string{"app_mention", "message.channels", "message.groups"}},
	{Key: FeatureDM, Label: "Direct messages", Need: NeedAlways,
		BotScopes: []string{"im:history", "im:read", "im:write"},
		Events:    []string{"message.im"}},
	{Key: FeatureGroupDM, Label: "Group DMs", Need: NeedAlways,
		BotScopes: []string{"mpim:history", "mpim:read"},
		Events:    []string{"message.mpim"}},
	{Key: FeatureIdentity, Label: "Sender identity", Need: NeedAlways,
		BotScopes: []string{"users:read", "users:read.email"}},
	{Key: FeatureGroupAccess, Label: "User-group access control", Need: NeedWhenOn,
		BotScopes: []string{"usergroups:read"}},
	{Key: FeatureReactionReply, Label: "🤖 auto-reply", Need: NeedWhenOn,
		BotScopes: []string{"reactions:read", "reactions:write"},
		Events:    []string{"reaction_added", "reaction_removed"}},
	{Key: FeatureAgentView, Label: "Agent view and status banner", Need: NeedWhenOn,
		BotScopes: []string{"assistant:write"},
		Events:    []string{"assistant_thread_started"}},
	{Key: FeatureFiles, Label: "Files and attachments", Need: NeedOptional,
		BotScopes: []string{"files:read", "files:write"}},
	{Key: FeatureCanvasList, Label: "Canvases and lists", Need: NeedInfo,
		BotScopes: []string{"canvases:read", "canvases:write", "lists:read", "lists:write"}},
}

// Requirements returns a copy of the feature table.
func Requirements() []Feature {
	out := make([]Feature, len(requirements))
	for i, f := range requirements {
		f.BotScopes = append([]string(nil), f.BotScopes...)
		f.Events = append([]string(nil), f.Events...)
		out[i] = f
	}
	return out
}

// RequiredBotScopes is the sorted union of every feature's bot scopes —
// what a generated manifest declares.
func RequiredBotScopes() []string { return union(func(f Feature) []string { return f.BotScopes }) }

// RequiredBotEvents is the sorted union of every feature's events.
func RequiredBotEvents() []string { return union(func(f Feature) []string { return f.Events }) }

func union(pick func(Feature) []string) []string {
	set := map[string]struct{}{}
	for _, f := range requirements {
		for _, v := range pick(f) {
			set[v] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// FeatureActive reports whether a NeedWhenOn feature is switched on for
// cfg. agentView is true for an instance that serves a Team agent (the
// agent view and its suggested prompts only exist there). Features of any
// other Need are always active.
func FeatureActive(key string, cfg agentconfig.SlackChannelConfig, agentView bool) bool {
	switch key {
	case FeatureGroupAccess:
		return cfg.GroupsMode == "whitelist"
	case FeatureReactionReply:
		return cfg.ReactionTriggerEnabled
	case FeatureAgentView:
		return agentView
	}
	return true
}
