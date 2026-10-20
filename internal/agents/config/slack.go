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
	Mode string `wick:"dropdown=socket|http;hidden;key=mode;group=Connection|Transport credentials. Socket mode needs the bot + app token. HTTP mode needs the bot token + signing secret and a public URL.;desc=Connection mode."`
	// Enabled is the instance's on/off switch on the Channels page. "" (a
	// row saved before the switch existed) counts as on; only "false"
	// stops the bot, and saving a token never turns it back on.
	Enabled       string `wick:"bool;default=true;hidden;key=enabled;group=Connection;desc=Run this Slack bot. Off stops it right away and keeps it stopped across restarts. The tokens stay stored. Turn it back on to reconnect — refused while a Team agent holds the same bot. If a Team agent used this app with a rotating app token (xoxe.xapp-), paste fresh tokens first: the stored refresh token has been spent."`
	BotToken      string `wick:"secret;hidden;key=bot_token;group=Connection;desc=Bot token (xoxb-...)."`
	AppToken      string `wick:"secret;hidden;key=app_token;group=Connection;desc=App-level token (xapp-... or xoxe.xapp-... when token rotation is on). Required for socket mode."`
	SigningSecret string `wick:"secret;hidden;key=signing_secret;group=Connection;desc=Signing secret. Required for http mode."`

	// Token rotation (xoxe.xapp- app tokens). wick refreshes the app token
	// itself through oauth.v2.access and writes the new pair back here.
	AppRefreshToken string `wick:"secret;hidden;key=app_refresh_token;group=Token Rotation|Only needed when Token rotation is ON in your Slack app (app token starts with xoxe.xapp-). Leave empty otherwise.;desc=Refresh token (xoxe-1-...) issued with a rotating app-level token. Required when the app token starts with xoxe.xapp-."`
	ClientID        string `wick:"hidden;key=client_id;group=Token Rotation;desc=Slack app Client ID (Basic Information - App Credentials). Required for a rotating app token."`
	ClientSecret    string `wick:"secret;hidden;key=client_secret;group=Token Rotation;desc=Slack app Client Secret (Basic Information - App Credentials). Required for a rotating app token."`
	// AppTokenExpiresAt is internal: RFC 3339 expiry of the current rotating
	// app token, written by wick after each refresh. Never set by hand, and
	// the `internal` flag keeps it off every config page.
	AppTokenExpiresAt string `wick:"hidden;internal;key=app_token_expires_at;group=Token Rotation;desc=Internal - expiry of the current rotating app token (RFC 3339). Written by wick after each refresh."`

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
	// ThreadCatchUpEnabled is opt-in: off for every bot until turned on.
	ThreadCatchUpEnabled bool `wick:"bool;hidden;key=thread_catchup_enabled;group=Agent Behaviour;desc=Catch the agent up on the thread. Off (default): a mention or the 🤖 switch wakes an existing thread session with only the message that woke it. On: that message is preceded by the replies posted in the thread since the previous waking message (anyone's, the bot's own excluded, up to 30 messages / 8000 bytes, oldest dropped first), marked as data from the thread, not instructions. Costs one conversations.replies call per such turn."`

	ReactionTriggerEnabled bool   `wick:"bool;hidden;key=reaction_trigger_enabled;group=Reaction Auto-Reply|Listen mode. Put 🤖 on a thread's top message, or ask the agent to listen, and it hears every new reply without a mention: each one is held for the agent's delay, the agent answers only what is meant for it and adds only what is missing when someone else already answered. Stop with a bare stop reply, by removing your 🤖 or clicking the bot's 🤖, or from the chat header. Threads are still started by @mention only, so this never creates a new session.\nSlack app must subscribe to events reaction_added, reaction_removed, and message.channels, with scopes reactions:read + channels:history. The shipped slack-app-manifest.json already includes them.;desc=Enable the 🤖 auto-reply switch."`
	ReactionChannelsMode   string `wick:"dropdown=all|whitelist;default=whitelist;hidden;key=reaction_channels_mode;visible_when=reaction_trigger_enabled:true;group=Reaction Auto-Reply;desc=Which channels honour the 🤖 switch. all = any channel the bot is in. whitelist = only the channels listed below."`
	ReactionChannels       string `wick:"picker=slack.channels;hidden;key=reaction_channels;visible_when=reaction_channels_mode:whitelist;group=Reaction Auto-Reply;desc=Channels where the 🤖 switch is active. Independent of the access whitelist."`
	// ListenManualChannels: client-facing channels where listening is only
	// ever switched on by a person, thread by thread.
	ListenManualChannels string `wick:"picker=slack.channels;hidden;key=listen_manual_channels;visible_when=reaction_trigger_enabled:true;group=Reaction Auto-Reply;desc=Channels where a new thread does NOT start listening on its own and an agent's channel/all listen scope never applies: someone has to put 🤖 on the thread or ask the agent to listen. Use it for client-facing channels. A channel rule with mode manual does the same."`
	// ReplyMode is what a 🤖 thread does with a reply that does not mention
	// the agent. Empty (every row saved before the field existed) = listen.
	ReplyMode string `wick:"dropdown=listen|auto_reply;default=listen;hidden;key=reply_mode;visible_when=reaction_trigger_enabled:true;group=Reaction Auto-Reply;desc=What a 🤖 thread does with a reply that does not mention the agent. listen = held for the listen delay, then the agent answers only what is meant for it. auto_reply = held only for the merge delay, then always answered, like a mention. A channel rule below wins over it."`
	// ListenDelaySec: empty = the wick LISTEN.md decides.
	ListenDelaySec    string `wick:"hidden;key=listen_delay_sec;visible_when=reaction_trigger_enabled:true;group=Reaction Auto-Reply;desc=Default listen delay in seconds. Empty = the delay in the wick LISTEN.md. An agent's LISTEN.md, a channel rule and a thread's own listen request win over it."`
	AutoReplyDelaySec string `wick:"hidden;key=auto_reply_delay_sec;default=3;visible_when=reaction_trigger_enabled:true;group=Reaction Auto-Reply;desc=Auto-reply merge delay in seconds: replies posted within it reach the agent as one turn, each new one restarting it, never longer than 30 seconds in all. 0 = at once."`
	// ChannelReplyRules: a JSON list of {id, name, mode, delay}, mode one of
	// listen | auto_reply | manual, delay in seconds ("" = the default).
	ChannelReplyRules string `wick:"kvlist=id|name|mode|delay;hidden;key=channel_reply_rules;visible_when=reaction_trigger_enabled:true;group=Reaction Auto-Reply;desc=Per-channel mode and delay over the defaults above. mode is listen, auto_reply or manual (a new thread never starts on its own, like Listen manual channels). delay is in seconds, empty = the default for that mode."`

	GateApprovers      string `wick:"dropdown=trigger_users|admins|custom;default=trigger_users;hidden;key=gate_approvers;group=Approval Gates|Who may resolve interactive approval gate buttons. trigger_users = anyone who passed the access checks.;desc=Who may resolve approval gates."`
	GateApproverUsers  string `wick:"picker=slack.users;hidden;key=gate_approver_users;visible_when=gate_approvers:custom;group=Approval Gates;desc=Custom approver users."`
	GateApproverGroups string `wick:"picker=slack.usergroups;hidden;key=gate_approver_groups;visible_when=gate_approvers:custom;group=Approval Gates;desc=Custom approver user groups."`

	ProjectID string `wick:"dropdown;hidden;key=project_id;group=Routing;desc=Project to use for sessions from this Slack channel. Leave empty to use the global default."`
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
