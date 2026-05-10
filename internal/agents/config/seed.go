package config

import pkgentity "github.com/yogasw/wick/pkg/entity"

// SeedGeneralConfig returns the StructToConfigs rows for GeneralConfig.
func SeedGeneralConfig() []pkgentity.Config {
	return pkgentity.StructToConfigs(DefaultGeneralConfig())
}

// SeedGateConfig returns the StructToConfigs rows for GateConfig.
func SeedGateConfig() []pkgentity.Config {
	return pkgentity.StructToConfigs(DefaultGateConfig())
}

// SeedSlackChannelConfig returns UI field metadata (key, label, type, description)
// for the Slack channel form. Used ONLY for rendering the config page —
// values come from agent_channels table, not from this seed.
func SeedSlackChannelConfig() []pkgentity.Config {
	return pkgentity.StructToConfigs(DefaultSlackChannelConfig())
}

// SeedTelegramChannelConfig returns UI field metadata (key, label, type, description)
// for the Telegram channel form. Used ONLY for rendering the config page —
// values come from agent_channels table, not from this seed.
func SeedTelegramChannelConfig() []pkgentity.Config {
	return pkgentity.StructToConfigs(DefaultTelegramChannelConfig())
}

// SeedWorkspaceConfig is the workspace counterpart.
func SeedWorkspaceConfig() []pkgentity.Config {
	return pkgentity.StructToConfigs(DefaultWorkspaceConfig())
}
