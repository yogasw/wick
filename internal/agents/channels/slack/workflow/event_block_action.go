package workflow

import (
	"github.com/yogasw/wick/internal/agents/workflow/integration"
	"github.com/yogasw/wick/internal/entity"
	"github.com/yogasw/wick/pkg/wickdocs"
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
		Docs: wickdocs.Docs{
			OutputShape: map[string]string{
				"payload.action_id":    "Per-element identifier set when building the block. Primary routing key.",
				"payload.value":        "Value of the clicked button or selected menu item.",
				"payload.selected_opt": "Static-select / overflow menu's chosen option (the value field of the option).",
				"payload.trigger_id":   "Short-lived (3s) token usable by open_modal / push_modal. Burns on first use.",
				"payload.response_url": "URL accepting up to 5 follow-up posts within 30 minutes. Use respond_url action.",
				"payload.state":        "View state map when the action originated inside a modal — block_id → action_id → typed value.",
			},
			Quirks: []string{
				"trigger_id MUST be consumed within 3 seconds of the click — any agent / classify / shell node between this event and open_modal will burn it.",
				"response_url stays valid for 30 minutes and accepts up to 5 posts; use it for in-place message updates outside the original chat.write flow.",
				"value is empty for menu interactions — use selected_opt instead for static_select / overflow / external menus.",
				"state is only populated when the action fires from inside a modal; channel-level button clicks return state == nil.",
			},
			PairWith: []string{
				"channel:slack.open_modal",
				"channel:slack.push_modal",
				"channel:slack.respond_url",
				"channel:slack.update_message",
			},
			CommonPitfalls: []string{
				"Don't run an agent before open_modal in the same path — trigger_id expires. Pattern: action → open_modal (skeleton) → agent → update_modal.",
				"Don't whitelist by action_id and forget channel_id — without channel filter every channel that hosts the block fires.",
			},
			InputSample:  `{"mode":"whitelist","action_id":"create_ticket"}`,
			OutputSample: `{"type":"channel","channel":"slack","event":"block_action","at":"2026-05-19T10:34:02Z","payload":{"user":"U02ABCDEF","action_id":"create_ticket","block_id":"actions1","value":"open","channel_id":"C12345","message_ts":"1700001234.005600","trigger_id":"123.4567.8abc","response_url":"https://hooks.slack.com/actions/T123/...","state":null}}`,
			Examples: []wickdocs.Example{
				{
					Name: "route_by_action_id",
					YAML: `- type: channel
  channel: slack
  event: block_action
  entry_node: open_modal_skeleton
  match_enabled: true
  match:
    mode: whitelist
    action_id: create_ticket`,
				},
			},
		},
	})
}
