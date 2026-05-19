package workflow

import (
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// ShortcutEvent fires for both global shortcuts (App menu) and message
// shortcuts (a message's ⋮ menu). Type is "shortcut" for global,
// "message_action" for message shortcuts. CallbackID is the shortcut
// identifier configured in the Slack app settings.
type ShortcutEvent struct {
	User       string         `json:"user"`
	Type       string         `json:"type"` // "shortcut" | "message_action"
	CallbackID string         `json:"callback_id"`
	TriggerID  string         `json:"trigger_id"`
	ChannelID  string         `json:"channel_id"`   // message_action only
	MessageTS  string         `json:"message_ts"`   // message_action only
	MessageRaw map[string]any `json:"message_raw"`  // message_action only — the message itself
}

func registerEventShortcut(reg *integration.Registry) {
	reg.RegisterEvent(integration.EventDescriptor{
		Channel:     Channel,
		Event:       "shortcut",
		Name:        "Slack: Shortcut invoked",
		Description: "Fires for both global app shortcuts and message shortcuts. Match by callback_id to route per shortcut.",
		PayloadType: ShortcutEvent{},
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{
				"payload.type":        "\"shortcut\" for global (app menu), \"message_action\" for message-context shortcuts.",
				"payload.callback_id": "Identifier set in Slack app settings. Primary routing key.",
				"payload.trigger_id":  "3-second-expiry token. Use for open_modal immediately.",
				"payload.channel_id":  "Message shortcut only — channel of the source message.",
				"payload.message_ts":  "Message shortcut only — ts of the source message. Pair with the slack connector get_permalink op.",
				"payload.message_raw": "Message shortcut only — full message payload Slack delivered.",
			},
			Quirks: []string{
				"Same trigger_id 3s expiry as block_action / command. Open modals on a short path.",
				"Message shortcut type carries source message context (channel, ts, raw). Global shortcut type carries only callback_id + user.",
			},
			PairWith: []string{
				"channel:slack.open_modal",
			},
			OutputSample: `{"type":"channel","channel":"slack","event":"shortcut","payload":{"user":"U02ABCDEF","type":"message_action","callback_id":"escalate_msg","trigger_id":"123.4567.8abc","channel_id":"C12345","message_ts":"1700001234.005600","message_raw":{"text":"the message body","ts":"1700001234.005600","user":"U03XYZ"}}}`,
			Examples: []wickdocs.Example{
				{
					Name: "escalate_message_shortcut",
					YAML: `- type: channel
  channel: slack
  event: shortcut
  entry_node: open_escalation_modal
  match_enabled: true
  match:
    mode: whitelist
    callback_id: escalate_msg`,
				},
			},
		},
	})
}
