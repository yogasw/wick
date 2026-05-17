package config

// SlackChannelConfig holds Slack transport credentials and access control.
// See agents-design.md §8.2.
//
// Access control is per-resource: each of Users / Groups / Channels has its
// own *Mode (all|whitelist) and its own picker-backed allow list. A request
// passes when every whitelist that is active also contains the requester.
//
// Approval gates have their own approver block: GateApprovers selects the
// role family (anyone who passed access / workspace admins / a custom list)
// allowed to resolve interactive gate buttons.
type SlackChannelConfig struct {
	Mode          string `wick:"dropdown=socket|http;hidden;key=mode;desc=Connection mode."`
	BotToken      string `wick:"secret;hidden;key=bot_token;desc=Bot token (xoxb-...)."`
	AppToken      string `wick:"secret;hidden;key=app_token;desc=App token (xapp-...). Required for socket mode."`
	SigningSecret string `wick:"secret;hidden;key=signing_secret;desc=Signing secret. Required for http mode."`

	UsersMode       string `wick:"dropdown=all|whitelist;default=all;hidden;key=users_mode;desc=Restrict which Slack users can trigger agents. Combined with groups via OR — pass when either matches."`
	AllowedUsers    string `wick:"picker=slack.users;hidden;key=allowed_users;visible_when=users_mode:whitelist;desc=Allowed users."`
	GroupsMode      string `wick:"dropdown=all|whitelist;default=all;hidden;key=groups_mode;desc=Restrict which user groups can trigger agents. Combined with users via OR."`
	AllowedGroups   string `wick:"picker=slack.usergroups;hidden;key=allowed_groups;visible_when=groups_mode:whitelist;desc=Allowed user groups."`
	ChannelsMode    string `wick:"dropdown=all|whitelist;default=all;hidden;key=channels_mode;desc=Restrict which channels can trigger agents."`
	AllowedChannels string `wick:"picker=slack.channels;hidden;key=allowed_channels;visible_when=channels_mode:whitelist;desc=Allowed channels."`

	GateApprovers      string `wick:"dropdown=trigger_users|admins|custom;default=trigger_users;hidden;key=gate_approvers;desc=Who may resolve approval gates. trigger_users = anyone who passed the access checks."`
	GateApproverUsers  string `wick:"picker=slack.users;hidden;key=gate_approver_users;visible_when=gate_approvers:custom;desc=Custom approver users."`
	GateApproverGroups string `wick:"picker=slack.usergroups;hidden;key=gate_approver_groups;visible_when=gate_approvers:custom;desc=Custom approver user groups."`

	Workspace string `wick:"dropdown;hidden;key=workspace;desc=Workspace to use for sessions from this Slack channel. Leave empty to use the global default."`
}

// DefaultSlackChannelConfig returns the empty Slack defaults. Slack stays off
// until the operator sets a token. Per-list modes default to "all" via the
// `default=all` wick tag on each field, so first-boot config is permissive.
func DefaultSlackChannelConfig() SlackChannelConfig {
	return SlackChannelConfig{
		Mode:          "socket",
		UsersMode:     "all",
		GroupsMode:    "all",
		ChannelsMode:  "all",
		GateApprovers: "trigger_users",
	}
}
