package workflow

import (
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/entity"
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
	})
}
