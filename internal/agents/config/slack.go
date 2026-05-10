package config

// SlackChannelConfig holds Slack transport credentials and access control.
// See agents-design.md §8.2.
type SlackChannelConfig struct {
	Mode          string `wick:"dropdown=socket|http;hidden;key=mode;desc=Connection mode."`
	BotToken      string `wick:"secret;hidden;key=bot_token;desc=Bot token (xoxb-...)."`
	AppToken      string `wick:"secret;hidden;key=app_token;desc=App token (xapp-...). Required for socket mode."`
	SigningSecret string `wick:"secret;hidden;key=signing_secret;desc=Signing secret. Required for http mode."`
	AccessMode    string `wick:"dropdown=everyone|users|groups;hidden;key=access_mode;desc=Who can trigger agents."`
	AllowedUsers  string `wick:"kvlist;hidden;key=allowed_users;desc=Allowed Slack user IDs. Active when access mode = users."`
	AllowedGroups string `wick:"kvlist;hidden;key=allowed_groups;desc=Allowed Slack user group IDs. Active when access mode = groups."`
	Workspace     string `wick:"dropdown;hidden;key=workspace;desc=Workspace to use for sessions from this Slack channel. Leave empty to use the global default."`
}

// DefaultSlackChannelConfig returns the empty Slack defaults. Slack stays off
// until the operator sets a token.
func DefaultSlackChannelConfig() SlackChannelConfig {
	return SlackChannelConfig{Mode: "socket", AccessMode: "everyone"}
}
