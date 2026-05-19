package workflow

import (
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// AppMentionEvent fires when the bot is @-mentioned in a channel.
// Text is already stripped of the leading <@BOTID> prefix.
type AppMentionEvent struct {
	User      string `json:"user"`
	Text      string `json:"text"`
	ChannelID string `json:"channel_id"`
	Thread    string `json:"thread"`
	TS        string `json:"ts"`
}

func registerEventAppMention(reg *integration.Registry) {
	reg.RegisterEvent(integration.EventDescriptor{
		Channel:     Channel,
		Event:       "app_mention",
		Name:        "Slack: Bot mentioned",
		Description: "Fires when the bot is @-mentioned in a channel. Text has the leading mention stripped.",
		PayloadType: AppMentionEvent{},
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{
				"payload.text":       "Message text with the leading <@BOTID> mention already stripped by wick.",
				"payload.user":       "Slack user ID of the mentioning user.",
				"payload.channel_id": "Channel where the mention happened.",
				"payload.thread":     "Thread root ts, or empty when mention is top-level.",
				"payload.ts":         "Mention message ts.",
			},
			Quirks: []string{
				"Leading <@BOTID> is stripped before reaching the workflow. Use the raw slack.message event if you need it.",
				"Repeated mentions in the same thread still fire one event per message — dedup downstream if needed.",
			},
			PairWith: []string{
				"channel:slack.send_message",
				"channel:slack.add_reaction",
			},
			OutputSample: `{"type":"channel","channel":"slack","event":"app_mention","payload":{"user":"U02ABCDEF","text":"check the staging deploy","channel_id":"C12345","thread":"1700001234.005600","ts":"1700001234.005600"}}`,
			Examples: []wickdocs.Example{
				{
					Name: "mention_handler",
					YAML: `- type: channel
  channel: slack
  event: app_mention
  entry_node: classify`,
				},
			},
		},
	})
}
