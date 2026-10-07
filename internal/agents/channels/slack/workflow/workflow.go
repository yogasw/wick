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
	"context"

	"github.com/yogasw/wick/internal/agents/channels/slack"
	"github.com/yogasw/wick/internal/agents/workflow/integration"
)

// ChannelPicker resolves the Slack instance an action should run as, for
// one run's context. One process hosts one bot per owning user, so
// "the" Slack channel is not a single object: replying as a bot other
// than the one that received the trigger posts under the wrong identity,
// or fails outright with not_in_channel.
//
// Returning nil means "no usable instance" — the action surfaces the
// same "slack channel not configured" error it always did.
type ChannelPicker func(ctx context.Context) *slack.Channel

// StaticPicker always resolves to ch. For single-instance callers and
// for the stdio MCP path, where a stub channel backs schema discovery
// and no run ever actually fires.
func StaticPicker(ch *slack.Channel) ChannelPicker {
	return func(context.Context) *slack.Channel { return ch }
}

// Channel is the slack module name used as the descriptor Channel field
// across every descriptor in this package. Centralized so a future
// rename only touches one place.
const Channel = "slack"

// RegisterAll registers every Slack event + action descriptor with the
// workflow integration registry. Actions bind to a ChannelPicker rather
// than a fixed instance, so each Execute closure reaches the live API
// client of the bot that belongs to the run.
//
// Call once at boot, after the channel registry is populated but before
// the engine starts serving runs.
func RegisterAll(reg *integration.Registry, pick ChannelPicker) {
	if reg == nil {
		return
	}
	// Events — inbound (Slack → workflow trigger).
	registerEventMessage(reg)
	registerEventThreadStarted(reg)
	registerEventAppMention(reg)
	registerEventAppHomeOpened(reg)
	registerEventBlockAction(reg)
	registerEventViewSubmission(reg)
	registerEventViewClosed(reg)
	registerEventShortcut(reg)
	registerEventCommand(reg)

	// Actions — outbound (workflow node → Slack API). Bound to pick so
	// each Execute closure dispatches against the run's own bot.
	registerActionSendMessage(reg, pick)
	registerActionSendToSession(reg, pick)
	registerActionSendEphemeral(reg, pick)
	registerActionUpdateMessage(reg, pick)
	registerActionAddReaction(reg, pick)
	registerActionOpenModal(reg, pick)
	registerActionUpdateModal(reg, pick)
	registerActionPushModal(reg, pick)
	registerActionPublishHome(reg, pick)
	registerActionRespondURL(reg, pick)
	registerActionOpenDM(reg, pick)
}
