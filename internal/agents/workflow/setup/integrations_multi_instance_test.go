package setup

import (
	"context"
	"testing"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentslack "github.com/yogasw/wick/internal/agents/channels/slack"
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/agents/workflow/trigger"
)

// One process hosts one Slack instance per owning user (registry
// AddKeyed, "slack:<user-id>"), each with its own socket / webhook feed.
// RegisterSlackIntegration used to wire the workflow event sink onto
// ChannelByName("slack") — whichever instance was registered FIRST — so
// every other bot's events reached no trigger at all, and a workflow
// with a channel trigger silently stopped firing once a second bot was
// configured.
func TestRegisterSlackIntegration_WiresEveryInstance(t *testing.T) {
	reg := agentchannels.NewRegistry()
	instances := map[string]*agentslack.Channel{
		"slack:__owner__": agentslack.NewWithOwner(agentconfig.SlackChannelConfig{}, ""),
		"slack:user-a":    agentslack.NewWithOwner(agentconfig.SlackChannelConfig{}, "user-a"),
		"slack:user-b":    agentslack.NewWithOwner(agentconfig.SlackChannelConfig{}, "user-b"),
	}
	for key, ch := range instances {
		reg.AddKeyed(key, ch, nil)
	}

	RegisterSlackIntegration(integration.New(), reg, trigger.NewRouter(nil, nil), nil)

	for key, ch := range instances {
		if !ch.HasWorkflowEventSink() {
			t.Errorf("%s: workflow event sink not wired — this bot's Slack events fire no channel trigger", key)
		}
	}
}

// A Slack instance created after boot (dashboard "add bot", hot reload)
// goes through AttachSlackWorkflowSink rather than the boot loop; without
// it the new bot receives Slack events but fires no trigger until restart.
func TestAttachSlackWorkflowSink(t *testing.T) {
	ch := agentslack.NewWithOwner(agentconfig.SlackChannelConfig{}, "user-c")
	if ch.HasWorkflowEventSink() {
		t.Fatal("fresh instance should start with no sink")
	}
	AttachSlackWorkflowSink(ch, trigger.NewRouter(nil, nil))
	if !ch.HasWorkflowEventSink() {
		t.Error("AttachSlackWorkflowSink did not wire the sink")
	}
}

// An action must run as the bot that received the trigger. Before this,
// RegisterAll bound every action closure to one fixed instance, so a
// workflow triggered by bot B replied as bot A — wrong identity, and
// not_in_channel whenever A wasn't in the target channel.
//
// The match itself is covered by slack.TestInstanceForBot (that package
// can set a resolved bot id); here we pin the fallback contract, since a
// run with no Slack trigger must keep working rather than start erroring.
func TestSlackInstancePicker_FallsBack(t *testing.T) {
	reg := agentchannels.NewRegistry()
	fallback := agentslack.NewWithOwner(agentconfig.SlackChannelConfig{}, "")
	reg.AddKeyed("slack:__owner__", fallback, nil)
	reg.AddKeyed("slack:user-a", agentslack.NewWithOwner(agentconfig.SlackChannelConfig{}, "user-a"), nil)

	pick := SlackInstancePicker(reg, fallback)

	cases := []struct {
		name    string
		ctx     context.Context
		payload map[string]any
		bare    bool
	}{
		{name: "no bot_user_id in payload", payload: map[string]any{"channel_id": "C1"}},
		{name: "unknown bot id", payload: map[string]any{"bot_user_id": "U0GONE"}},
		{name: "empty bot id", payload: map[string]any{"bot_user_id": ""}},
		{name: "non-string bot id", payload: map[string]any{"bot_user_id": 42}},
		{name: "nil payload", payload: nil},
		{name: "cron / manual run, no payload in ctx", bare: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if !tc.bare {
				ctx = integration.WithTriggerPayload(ctx, tc.payload)
			}
			if got := pick(ctx); got != fallback {
				t.Errorf("picked %p, want the bound fallback %p", got, fallback)
			}
		})
	}
}

// A nil registry must not panic — the stdio MCP path has no registry.
func TestSlackInstancePicker_NilRegistry(t *testing.T) {
	fallback := agentslack.NewWithOwner(agentconfig.SlackChannelConfig{}, "")
	got := SlackInstancePicker(nil, fallback)(
		integration.WithTriggerPayload(context.Background(), map[string]any{"bot_user_id": "U0AAA"}),
	)
	if got != fallback {
		t.Error("nil registry should fall back")
	}
}
