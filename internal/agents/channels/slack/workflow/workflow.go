// Package workflow registers Slack's per-event triggers + per-action
// op descriptors with the workflow integration registry. Each event and
// action lives in its own file so adding a new one is a single-file
// change — no engine edit, no palette edit, no router edit. The wiring
// is mechanical: bind the descriptor's Execute closure to the Slack
// channel's API client, register the descriptor, done.
//
// Layout convention:
//
//	event_<name>.go   ← one inbound event class
//	  exports a typed payload struct (used for schema gen) and a
//	  `register<Name>` func that pushes the descriptor into the
//	  integration registry.
//
//	action_<name>.go  ← one outbound op
//	  exports a typed Input + Output struct (used for schema gen and
//	  for documenting the args block in workflow YAML) and a
//	  `register<Name>` func that registers the descriptor with an
//	  Execute closure bound to the channel.
//
// RegisterAll is the single entry point a setup composer calls at
// boot: it wires every event + action descriptor for Slack.
package workflow

import (
	"github.com/yogasw/wick/internal/agents/channels/slack"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
)

// Channel is the slack module name used as the descriptor Channel field
// across every descriptor in this package. Centralized so a future
// rename only touches one place.
const Channel = "slack"

// RegisterAll registers every Slack event + action descriptor with the
// workflow integration registry. Bind to a concrete *slack.Channel so
// action Execute closures can reach the live API client + config.
//
// Call once at boot, after slack.Channel is constructed but before the
// engine starts serving runs.
func RegisterAll(reg *integration.Registry, ch *slack.Channel) {
	if reg == nil {
		return
	}
	// Events — inbound (Slack → workflow trigger).
	registerEventMessage(reg)
	registerEventAppMention(reg)
	registerEventAppHomeOpened(reg)
	registerEventBlockAction(reg)
	registerEventViewSubmission(reg)
	registerEventViewClosed(reg)
	registerEventShortcut(reg)
	registerEventCommand(reg)

	// Actions — outbound (workflow node → Slack API). Bound to ch so
	// each Execute closure dispatches against the live api client.
	registerActionSendMessage(reg, ch)
	registerActionSendEphemeral(reg, ch)
	registerActionUpdateMessage(reg, ch)
	registerActionAddReaction(reg, ch)
	registerActionOpenModal(reg, ch)
	registerActionUpdateModal(reg, ch)
	registerActionPushModal(reg, ch)
	registerActionPublishHome(reg, ch)
	registerActionRespondURL(reg, ch)
	registerActionOpenDM(reg, ch)
}
