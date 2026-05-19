package workflow

import (
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// CommandEvent fires when a user invokes a slash command (e.g.
// `/support-ticket some text`). Command includes the leading slash;
// Text is whatever the user typed after it.
type CommandEvent struct {
	User        string `json:"user"`
	Command     string `json:"command"` // "/support-ticket"
	Text        string `json:"text"`    // text after the command
	ChannelID   string `json:"channel_id"`
	TeamID      string `json:"team_id"`
	TriggerID   string `json:"trigger_id"`
	ResponseURL string `json:"response_url"`
}

func registerEventCommand(reg *integration.Registry) {
	reg.RegisterEvent(integration.EventDescriptor{
		Channel:     Channel,
		Event:       "command",
		Name:        "Slack: Slash command",
		Description: "Fires when a user invokes a slash command. Match by command (with leading slash) to route per command.",
		PayloadType: CommandEvent{},
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{
				"payload.command":      "Slash command including the leading slash (e.g. \"/support-ticket\").",
				"payload.text":         "Everything the user typed after the command. Whitespace-trimmed.",
				"payload.user":         "Invoking user ID.",
				"payload.channel_id":   "Channel where the command was invoked.",
				"payload.trigger_id":   "Short-lived (3s) trigger_id usable for open_modal.",
				"payload.response_url": "30-minute / 5-post response URL for in-place follow-ups.",
			},
			Quirks: []string{
				"trigger_id 3-second expiry applies just like block_action — open modals immediately.",
				"Slack requires an ack within 3 seconds. Wick's router acks synchronously and runs the workflow in the background, so long workflows don't time out the command but need to post via response_url or chat.postMessage to surface results.",
			},
			PairWith: []string{
				"channel:slack.open_modal",
				"channel:slack.respond_url",
				"channel:slack.send_ephemeral",
			},
			OutputSample: `{"type":"channel","channel":"slack","event":"command","payload":{"user":"U02ABCDEF","command":"/support-ticket","text":"payment refund issue","channel_id":"C12345","team_id":"T01ABCDEF","trigger_id":"123.4567.8abc","response_url":"https://hooks.slack.com/commands/T01/..."}}`,
			Examples: []wickdocs.Example{
				{
					Name: "open_modal_from_command",
					YAML: `- type: channel
  channel: slack
  event: command
  entry_node: open_ticket_modal`,
				},
			},
		},
	})
}
