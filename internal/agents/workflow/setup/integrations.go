package setup

import (
	"context"
	"time"

	agentchannels "github.com/yogasw/wick/internal/agents/channels"
	agentslack "github.com/yogasw/wick/internal/agents/channels/slack"
	slackwf "github.com/yogasw/wick/internal/agents/channels/slack/workflow"
	"github.com/yogasw/wick/internal/agents/workflow"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	wfmcp "github.com/yogasw/wick/internal/agents/workflow/mcp"
	"github.com/yogasw/wick/internal/agents/workflow/trigger"
)

// RegisterSlackIntegration wires the Slack channel into the workflow
// integration surface. Two pieces, both required for end-to-end:
//
//  1. ActionDescriptor / EventDescriptor registration — slackwf.RegisterAll
//     pushes every per-event + per-action descriptor (send_message,
//     open_modal, on_message, on_block_action, …) into intReg so the
//     palette + the engine see them.
//
//  2. Inbound event sink — wires slack.Channel.SetWorkflowEventSink so
//     every Slack callback (message, block_action, view_submission,
//     slash command, …) fires router.Dispatch with a normalised
//     workflow.Event.
//
// No-op if Slack isn't registered or the channel isn't the expected
// type — callers can call this unconditionally regardless of which
// channels are configured.
func RegisterSlackIntegration(intReg *integration.Registry, base *agentchannels.Registry, router *trigger.Router, pickers *wfmcp.PickerRegistry) {
	if intReg == nil || base == nil || router == nil {
		return
	}
	// One process hosts one Slack instance PER OWNING USER (registry
	// AddKeyed, "slack:<user-id>"), and every one of them has its own
	// socket/webhook feed. ChannelByName would hand back whichever was
	// registered first, so every other bot's messages would never reach
	// the router — channel triggers silently stop firing the moment a
	// second Slack bot is configured. Attach the sink to all of them.
	var first *agentslack.Channel
	for _, ch := range base.Channels() {
		slackCh, ok := ch.(*agentslack.Channel)
		if !ok {
			continue
		}
		if first == nil {
			first = slackCh
		}
		AttachSlackWorkflowSink(slackCh, router)
	}
	if first == nil {
		return
	}
	// Descriptors and picker sources are process-wide (one catalog for
	// the whole editor), so they bind once. Actions resolve their own
	// instance per run, and pickers fan out over every instance
	// internally — see slackwf.RegisterPickers.
	slackwf.RegisterAll(intReg, SlackInstancePicker(base, first))
	if pickers != nil {
		slackwf.RegisterPickers(pickers, base)
	}
}

// AttachSlackWorkflowSink points one Slack instance's inbound event sink
// at the workflow router. Exported so the hot-reload path in
// tools/agents can wire an instance created after boot — without it a
// bot added from the dashboard receives Slack events but fires no
// workflow trigger until the next restart.
//
// bot_user_id / bot_name ride along in the payload so a workflow can
// tell WHICH bot saw the event when several are connected to the same
// workspace.
func AttachSlackWorkflowSink(ch *agentslack.Channel, router *trigger.Router) {
	if ch == nil || router == nil {
		return
	}
	ch.SetWorkflowEventSink(func(ctx context.Context, event string, payload map[string]any) {
		if payload == nil {
			payload = map[string]any{}
		}
		if _, ok := payload["bot_user_id"]; !ok {
			payload["bot_user_id"] = ch.BotUserID()
		}
		if _, ok := payload["bot_name"]; !ok {
			payload["bot_name"] = ch.BotUserName()
		}
		router.Dispatch(ctx, workflow.Event{
			Type:    string(workflow.TriggerChannel),
			Subtype: event,
			Channel: "slack",
			At:      time.Now().UTC(),
			Payload: payload,
		})
	})
}

// SlackInstancePicker resolves which Slack bot an action node runs as.
//
// The run's trigger payload carries bot_user_id — the bot that received
// the event that started this run (see AttachSlackWorkflowSink). Sending
// as a different bot is not a cosmetic difference: it posts under an
// identity the user never talked to, and in a private channel the other
// bot isn't in it fails outright with not_in_channel.
//
// Falls back to `fallback` when the run has no Slack trigger (manual,
// cron, webhook) or when the recorded bot is no longer registered — an
// action that used to work must not start erroring because the payload
// lacks a key.
func SlackInstancePicker(base *agentchannels.Registry, fallback *agentslack.Channel) slackwf.ChannelPicker {
	return func(ctx context.Context) *agentslack.Channel {
		botID, _ := integration.TriggerPayload(ctx)["bot_user_id"].(string)
		if inst := agentslack.InstanceForBot(base, botID); inst != nil {
			return inst
		}
		return fallback
	}
}
