package config

// RestChannelConfig holds REST (OpenAI-compatible HTTP) channel settings.
// Auth uses Personal Access Tokens minted at /profile/tokens, so no token
// field is stored on the channel itself — every request carries its own
// Bearer.
type RestChannelConfig struct {
	Enabled   string `wick:"dropdown=true|false;hidden;key=enabled;desc=Enable the OpenAI-compatible REST endpoint at /integrations/rest/v1/chat/completions."`
	Workspace string `wick:"dropdown;hidden;key=workspace;desc=Workspace to use for sessions from REST requests. Leave empty to use the global default."`
}

// DefaultRestChannelConfig returns the empty REST defaults. REST stays off
// until the operator flips Enabled.
func DefaultRestChannelConfig() RestChannelConfig {
	return RestChannelConfig{Enabled: "false"}
}
