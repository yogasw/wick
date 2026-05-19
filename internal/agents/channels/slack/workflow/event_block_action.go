package workflow

import (
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/entity"
)

// SlackBlockActionMatch is the trigger filter form for slack.block_action events.
// Router matches payload.action_id and payload.channel_id against these lists.
//
// Use action_id whitelist to route different buttons to different workflows
// without needing a branch node.
type SlackBlockActionMatch struct {
	Mode            string `wick:"dropdown=all|whitelist;default=all;desc=Filter mode: all=fire every action; whitelist=only listed action IDs / channels"`
	ActionID        string `wick:"visible_when=mode:whitelist;key=action_id;desc=Exact action_id to match (e.g. create_ticket). Leave blank to allow any."`
	AllowedChannels string `wick:"picker=slack.channels;visible_when=mode:whitelist;key=channel_id;desc=Only fire from these channels"`
}

// BlockActionEvent fires when a user clicks a button, selects from a
// menu, or interacts with any Block Kit element. ActionID is the
// per-element identifier; CallbackID covers the legacy attachment-level
// callback. TriggerID is short-lived (3s) — only useful for opening a
// modal from the SAME run, downstream nodes that delay (LLM, network)
// will see it expire.
type BlockActionEvent struct {
	User        string         `json:"user"`
	ActionID    string         `json:"action_id"`
	CallbackID  string         `json:"callback_id"`
	BlockID     string         `json:"block_id"`
	Value       string         `json:"value"`        // button/menu value
	SelectedOpt string         `json:"selected_opt"` // selected option for menus
	ChannelID   string         `json:"channel_id"`
	MessageTS   string         `json:"message_ts"`
	Thread      string         `json:"thread"`
	TriggerID   string         `json:"trigger_id"`
	ResponseURL string         `json:"response_url"`
	State       map[string]any `json:"state"` // values from view state when in modal
	Raw         map[string]any `json:"raw"`   // full Slack callback verbatim
}

func registerEventBlockAction(reg *integration.Registry) {
	reg.RegisterEvent(integration.EventDescriptor{
		Channel:     Channel,
		Event:       "block_action",
		Name:        "Slack: Block action (button/menu)",
		Description: "Fires when a user clicks a button or selects a menu item. Use action_id or callback_id to route. trigger_id expires in 3s — open modals from this same run.",
		PayloadType: BlockActionEvent{},
		MatchSchema: entity.StructToConfigs(SlackBlockActionMatch{}),
	})
}
