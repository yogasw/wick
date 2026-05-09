package config

// SlackConfig holds Slack transport credentials and access control.
// See agents-design.md §8.2. Empty / disabled until phase 5.
type SlackConfig struct {
	Mode           string `wick:"dropdown=socket|http;desc=Connection mode."`
	BotToken       string `wick:"secret;required;desc=Bot token (xoxb-...)."`
	AppToken       string `wick:"secret;desc=App token (xapp-...). Required for socket mode."`
	SigningSecret  string `wick:"secret;desc=Signing secret. Required for http mode."`
	AccessMode     string `wick:"dropdown=everyone|users|groups;desc=Who can trigger agents."`
	AllowedUsers   string `wick:"kvlist;desc=Allowed Slack user IDs. Active when access mode = users."`
	AllowedGroups  string `wick:"kvlist;desc=Allowed Slack user group IDs. Active when access mode = groups."`
	SlackWorkspace string `wick:"dropdown;desc=Workspace to use for sessions from this Slack channel. Leave empty to use the global default."`
}

// DefaultSlackConfig returns the empty Slack defaults. Slack stays off
// until the operator sets a token.
func DefaultSlackConfig() SlackConfig {
	return SlackConfig{
		Mode:       "socket",
		AccessMode: "everyone",
	}
}
