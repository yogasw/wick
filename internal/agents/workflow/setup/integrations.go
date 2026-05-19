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
	ch := base.ChannelByName("slack")
	if ch == nil {
		return
	}
	slackCh, ok := ch.(*agentslack.Channel)
	if !ok {
		return
	}
	slackwf.RegisterAll(intReg, slackCh)
	if pickers != nil {
		slackwf.RegisterPickers(pickers, slackCh)
	}
	slackCh.SetWorkflowEventSink(func(ctx context.Context, event string, payload map[string]any) {
		router.Dispatch(ctx, workflow.Event{
			Type:    string(workflow.TriggerChannel),
			Subtype: event,
			Channel: "slack",
			At:      time.Now().UTC(),
			Payload: payload,
		})
	})
}
