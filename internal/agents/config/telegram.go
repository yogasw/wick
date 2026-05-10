package config

// TelegramChannelConfig holds Telegram bot credentials and access control.
// Each channel type has its own JSON blob in agent_channels, so keys need
// no prefix.
type TelegramChannelConfig struct {
	BotToken   string `wick:"secret;hidden;key=bot_token;desc=Bot token from @BotFather (format: 123456:ABC-...)."`
	AllowedIDs string `wick:"kvlist;hidden;key=allowed_ids;desc=Allowed Telegram chat IDs. Leave empty to allow all chats."`
	Workspace  string `wick:"dropdown;hidden;key=workspace;desc=Workspace to use for sessions from this Telegram channel. Leave empty to use the global default."`
}

// DefaultTelegramChannelConfig returns the empty Telegram defaults.
// Telegram stays off until the operator sets a bot token.
func DefaultTelegramChannelConfig() TelegramChannelConfig {
	return TelegramChannelConfig{}
}
