package config

import pkgentity "github.com/yogasw/wick/pkg/entity"

// SeedGeneralConfig returns the StructToConfigs rows for GeneralConfig
// using DefaultGeneralConfig as the seed values. Used by tool
// registration so the admin UI shows non-empty defaults on first boot.
func SeedGeneralConfig() []pkgentity.Config {
	return pkgentity.StructToConfigs(DefaultGeneralConfig())
}

// SeedSlackConfig is the Slack counterpart.
func SeedSlackConfig() []pkgentity.Config {
	return pkgentity.StructToConfigs(DefaultSlackConfig())
}

// SeedWorkspaceConfig is the Workspace counterpart.
func SeedWorkspaceConfig() []pkgentity.Config {
	return pkgentity.StructToConfigs(DefaultWorkspaceConfig())
}
