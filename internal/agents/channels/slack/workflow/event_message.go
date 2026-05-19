package workflow

import (
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/wickdocs"
)

// SlackMessageMatch is the trigger filter form rendered in the
// inspector for the slack.message event. Operator toggles "Filter
// events" on the trigger panel to apply these — disabled = dump-all,
// every message fires the workflow.
//
// Field semantics matched by the router:
//   - Mode = "all"       → channel_id / user fields hidden, no filter
//   - Mode = "whitelist" → only fire when message source matches the
//                          chip lists
//
// Picker entries serialize to `[{id,name}, ...]` JSON. Router checks
// payload.channel_id / payload.user as id-membership against the list.
type SlackMessageMatch struct {
	Mode            string `wick:"dropdown=all|whitelist;default=all;desc=Filter mode: all=fire every message; whitelist=only listed channels/users"`
	AllowedChannels string `wick:"picker=slack.channels;visible_when=mode:whitelist;key=channel_id;desc=Only fire from these channels"`
	AllowedUsers    string `wick:"picker=slack.users;visible_when=mode:whitelist;key=user;desc=Only from these users"`
	TextContains    string `wick:"desc=Case-insensitive substring filter (optional)"`
}

// MessageEvent is the payload shape the Slack channel emits when a
// user posts a message in a channel/DM the bot can see. Downstream
// nodes reference these fields via `{{.Trigger.<field>}}` templates.
type MessageEvent struct {
	User      string `json:"user"`       // sender user ID (U…)
	Text      string `json:"text"`       // raw message text
	ChannelID string `json:"channel_id"` // C… or D… (DM)
	Thread    string `json:"thread"`     // thread_ts (== ts when starting a new thread)
	TS        string `json:"ts"`         // message ts (Slack message ID)
	IsDM      bool   `json:"is_dm"`      // true for direct messages
}

func registerEventMessage(reg *integration.Registry) {
	reg.RegisterEvent(integration.EventDescriptor{
		Channel:     Channel,
		Event:       "message",
		Name:        "Slack: New message",
		Description: "Fires when a user posts a message in a channel/DM the bot can see (excluding bot's own messages).",
		PayloadType: MessageEvent{},
		MatchSchema: entity.StructToConfigs(SlackMessageMatch{}),
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{
				"payload.text":       "Raw message text the user posted. Whitespace untrimmed; mentions of the bot are NOT stripped — use replace / trim helpers downstream if needed.",
				"payload.user":       "Slack user ID (U…) of the sender. Resolve to email/name via the slack connector users.info / users.lookupByEmail ops.",
				"payload.channel_id": "Channel ID (C…) or DM ID (D…). DMs prefix with D; check payload.is_dm for the boolean.",
				"payload.thread":     "Thread root ts. Equal to payload.ts when this is the first message in a new thread.",
				"payload.ts":         "Message timestamp / Slack message ID. Pass to update_message / delete_message to target this exact message.",
				"payload.is_dm":      "True when channel_id starts with D (direct message). Use to branch DM-only paths.",
			},
			Quirks: []string{
				"Bot's own messages are filtered before reaching this event — no infinite loops from send_message.",
				"Wick normalises Slack's nested message payload into flat keys (text, user, channel_id, thread, ts, is_dm). The raw Slack payload still lives at payload.raw.* if you need fields wick didn't surface.",
				"Edits and deletes do NOT re-fire this event. Subscribe to message_changed / message_deleted (separate events) if you need to react to those.",
				"Filter activation needs BOTH MatchEnabled:true AND a non-empty Match map. Either alone is a no-op.",
				"Picker fields (channel_id, user) accept [{id, name}, …] objects only. Plain string arrays are silently rejected by the router.",
			},
			PairWith: []string{
				"channel:slack.send_message",
				"channel:slack.add_reaction",
				"channel:slack.open_modal",
			},
			CommonPitfalls: []string{
				"Don't try to read .Event.Text / .Event.User — those keys never existed. Use {{.Node.<trigger-label>.payload.text}} / .payload.user instead.",
				"Don't put a heavy agent node directly between this trigger and a Slack open_modal action — the original block_action trigger_id expires in 3s. For modals chained off a message, do a fast open_modal-first / agent-second / update_modal-third sequence.",
				"Don't use mode:whitelist with an empty channel_id list expecting \"any channel\" — the router treats empty whitelist as \"match nothing\".",
			},
			InputSample:  `{"mode":"whitelist","channel_id":[{"id":"C12345","name":"#support"}]}`,
			OutputSample: `{"type":"channel","channel":"slack","event":"message","at":"2026-05-19T10:32:17Z","payload":{"user":"U02ABCDEF","text":"hi @bot can you check the staging deploy?","channel_id":"C12345","thread":"1700001234.005600","ts":"1700001234.005600","is_dm":false}}`,
			Examples: []wickdocs.Example{
				{
					Name: "dump_all_messages",
					YAML: `- type: channel
  channel: slack
  event: message
  entry_node: classify
  match_enabled: false`,
				},
				{
					Name: "whitelist_two_channels",
					YAML: `- type: channel
  channel: slack
  event: message
  entry_node: classify
  match_enabled: true
  match:
    mode: whitelist
    channel_id:
      - { id: C12345, name: "#support" }
      - { id: C67890, name: "#bugs" }`,
				},
				{
					Name: "text_contains_filter",
					YAML: `- type: channel
  channel: slack
  event: message
  entry_node: route
  match_enabled: true
  match:
    mode: whitelist
    text_contains: "bug"`,
				},
			},
		},
	})
}
