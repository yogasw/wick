package config

// SlackChannelConfig holds Slack transport credentials and access control.
// See agents-design.md §8.2.
//
// Access control is per-resource: each of Users / Groups / Channels has its
// own *Mode (all|whitelist) and its own picker-backed allow list. Bots have
// their own BotsMode (none|whitelist|all), default none = bots are ignored.
// A request passes when every whitelist that is active also contains the requester.
//
// Approval gates have their own approver block: GateApprovers selects the
// role family (anyone who passed access / workspace admins / a custom list)
// allowed to resolve interactive gate buttons.
type SlackChannelConfig struct {
	Mode          string `wick:"dropdown=socket|http;hidden;key=mode;group=Connection|Transport credentials. Socket mode needs the bot + app token; HTTP mode needs the bot token + signing secret and a public URL.;desc=Connection mode."`
	BotToken      string `wick:"secret;hidden;key=bot_token;group=Connection;desc=Bot token (xoxb-...)."`
	AppToken      string `wick:"secret;hidden;key=app_token;group=Connection;desc=App token (xapp-...). Required for socket mode."`
	SigningSecret string `wick:"secret;hidden;key=signing_secret;group=Connection;desc=Signing secret. Required for http mode."`

	UsersMode       string `wick:"dropdown=all|whitelist;default=all;hidden;key=users_mode;group=Access Control|Who may trigger agents. Users, groups and bots are combined via OR (pass when any matches) and channels are an independent gate.;desc=Restrict which Slack users can trigger agents."`
	AllowedUsers    string `wick:"picker=slack.users;hidden;key=allowed_users;visible_when=users_mode:whitelist;group=Access Control;desc=Allowed users."`
	GroupsMode      string `wick:"dropdown=all|whitelist;default=all;hidden;key=groups_mode;group=Access Control;desc=Restrict which user groups can trigger agents."`
	AllowedGroups   string `wick:"picker=slack.usergroups;hidden;key=allowed_groups;visible_when=groups_mode:whitelist;group=Access Control;desc=Allowed user groups."`
	BotsMode        string `wick:"dropdown=none|whitelist|all;default=none;hidden;key=bots_mode;group=Access Control;desc=Whether messages from other Slack apps and bots may trigger agents. none = ignore every bot (default). whitelist = only the bots listed below. all = any bot. The agent's own bot never triggers itself and approval gates stay human-only."`
	AllowedBots     string `wick:"picker=slack.bots;hidden;key=allowed_bots;visible_when=bots_mode:whitelist;group=Access Control;desc=Allowed bots. Matched against the bot user ID, bot ID or app ID of the incoming message."`
	ChannelsMode    string `wick:"dropdown=all|whitelist;default=all;hidden;key=channels_mode;group=Access Control;desc=Restrict which channels can trigger agents."`
	AllowedChannels string `wick:"picker=slack.channels;hidden;key=allowed_channels;visible_when=channels_mode:whitelist;group=Access Control;desc=Allowed channels."`

	AskUserEnabled bool `wick:"bool;hidden;key=ask_user_enabled;group=Agent Behaviour;desc=Allow the ask_user MCP tool for sessions from this Slack channel. Off = the agent gets an error and picks a default (recommended until the ask is rendered in Slack — today the prompt only shows in the wick web UI)."`

	// HideSubAgentStatus is phrased as an opt-out so the zero value — every
	// channel row saved before the field existed — keeps the behaviour on.
	HideSubAgentStatus bool `wick:"bool;hidden;key=hide_subagent_status;group=Agent Behaviour;desc=Stay quiet about background sub-agents. Off (default): the thread gets one short message when a sub-agent is started in the background, and the assistant banner keeps showing which sub-agents are still working after the agent's own turn ends. On: neither."`

	ReactionTriggerEnabled bool   `wick:"bool;hidden;key=reaction_trigger_enabled;group=Reaction Auto-Reply|Put 🤖 on a thread's top message to auto-reply to every new reply without a mention — remove it to stop. Threads are still started by @mention only, so this never creates a new session.\nSlack app must subscribe to events reaction_added, reaction_removed, and message.channels, with scopes reactions:read + channels:history. The shipped slack-app-manifest.json already includes them.;desc=Enable the 🤖 auto-reply switch."`
	ReactionChannelsMode   string `wick:"dropdown=all|whitelist;default=whitelist;hidden;key=reaction_channels_mode;visible_when=reaction_trigger_enabled:true;group=Reaction Auto-Reply;desc=Which channels honour the 🤖 switch. all = any channel the bot is in. whitelist = only the channels listed below."`
	ReactionChannels       string `wick:"picker=slack.channels;hidden;key=reaction_channels;visible_when=reaction_channels_mode:whitelist;group=Reaction Auto-Reply;desc=Channels where the 🤖 switch is active. Independent of the access whitelist."`

	GateApprovers      string `wick:"dropdown=trigger_users|admins|custom;default=trigger_users;hidden;key=gate_approvers;group=Approval Gates|Who may resolve interactive approval gate buttons. trigger_users = anyone who passed the access checks.;desc=Who may resolve approval gates."`
	GateApproverUsers  string `wick:"picker=slack.users;hidden;key=gate_approver_users;visible_when=gate_approvers:custom;group=Approval Gates;desc=Custom approver users."`
	GateApproverGroups string `wick:"picker=slack.usergroups;hidden;key=gate_approver_groups;visible_when=gate_approvers:custom;group=Approval Gates;desc=Custom approver user groups."`

	ProjectID string `wick:"dropdown;hidden;key=project_id;group=Routing;desc=Project to use for sessions from this Slack channel. Leave empty to use the global default."`
	PublicURL string `wick:"hidden;key=public_url;group=Routing;desc=Public URL for Slack HTTP mode webhooks."`
}

// DefaultSlackChannelConfig returns the empty Slack defaults. Slack stays off
// until the operator sets a token. Per-list modes default to "all" via the
// `default=all` wick tag on each field, so first-boot config is permissive.
func DefaultSlackChannelConfig() SlackChannelConfig {
	return SlackChannelConfig{
		Mode:                 "socket",
		UsersMode:            "all",
		GroupsMode:           "all",
		ChannelsMode:         "all",
		BotsMode:             "none",
		ReactionChannelsMode: "whitelist",
		AskUserEnabled:       false,
		GateApprovers:        "trigger_users",
	}
}
