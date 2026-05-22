package channels

import (
	"context"
	"strings"

	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/provider"
)

// ProviderSwitchResult is returned by ParseProviderTag.
type ProviderSwitchResult struct {
	Tag    string // provider type parsed from #tag, empty if no tag
	Rest   string // message text after the tag (may be empty)
	HasTag bool
}

// ParseProviderTag checks if text starts with #<provider> and splits it.
// Returns HasTag=false if text does not start with '#'.
func ParseProviderTag(text string) ProviderSwitchResult {
	if !strings.HasPrefix(text, "#") {
		return ProviderSwitchResult{Rest: text}
	}
	parts := strings.SplitN(text, " ", 2)
	tag := strings.ToLower(strings.TrimPrefix(parts[0], "#"))
	rest := ""
	if len(parts) > 1 {
		rest = strings.TrimSpace(parts[1])
	}
	return ProviderSwitchResult{Tag: tag, Rest: rest, HasTag: true}
}

// SwitchProvider updates agents.json, records a system turn in
// conversation.jsonl, and kills the running agent so the next message
// spawns with the new provider. source is the transport label ("ui",
// "slack", etc.) written into conversation.jsonl.
func SwitchProvider(layout agentconfig.Layout, pool SwitchPool, sessionID, agentName, tag, source string) error {
	return provider.Switch(layout, pool, sessionID, agentName, tag, provider.SwitchOptions{Source: source})
}

// WrapSendFunc wraps a SendFunc to intercept #<provider> prefix.
// On switch-only message (no body), the send is skipped after confirmation.
// On switch+message, provider is switched then message is forwarded.
// On unknown provider, an error is surfaced via replyFn.
// replyFn, if non-nil, is called with the confirmation text so the channel
// (Slack, REST) can deliver it without forwarding to the provider.
func WrapSendFunc(fn SendFunc, layout agentconfig.Layout, pool SwitchPool, replyFn func(sessionID, agentName, source, text string)) SendFunc {
	return func(ctx context.Context, sessionID, agentName, source, role, text string) error {
		r := ParseProviderTag(text)
		if !r.HasTag {
			return fn(ctx, sessionID, agentName, source, role, text)
		}
		reply := func(t string) {
			if replyFn != nil {
				replyFn(sessionID, agentName, source, t)
			}
		}
		if err := provider.Switch(layout, pool, sessionID, agentName, r.Tag, provider.SwitchOptions{
			Source:   source,
			UserText: text,
			Reply:    reply,
		}); err != nil {
			reply("⚠️ " + err.Error())
			return nil
		}
		if r.Rest == "" {
			return nil
		}
		return fn(ctx, sessionID, agentName, source, role, r.Rest)
	}
}
