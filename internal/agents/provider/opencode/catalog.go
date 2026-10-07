package opencode

import "github.com/yogasw/wick/internal/agents/provider"

// init registers opencode's env + args picker catalog, from
// packages/opencode/src/cli/cmd/run.ts and web docs config.mdx (7945de2).
// XDG_DATA_HOME is absent on purpose: the instance owns it (accounts.go).
func init() {
	provider.RegisterCatalog(provider.TypeOpencode, provider.ProviderCatalog{
		Env: []provider.CatalogEntry{
			{Key: "OPENCODE_CONFIG", Kind: provider.CatalogString, Placeholder: "/path/opencode.json",
				Description: "Extra config file merged for every spawn."},
			{Key: "OPENCODE_CONFIG_DIR", Kind: provider.CatalogString, Placeholder: "/path/.opencode",
				Description: "Extra config dir (agents, commands, plugins)."},
			{Key: "OPENCODE_DISABLE_AUTOUPDATE", Kind: provider.CatalogBool, Options: []string{"true", "false"},
				Description: "Skip the self-update check."},
		},
		Args: []provider.CatalogEntry{
			{Key: "--model", Kind: provider.CatalogString, Placeholder: "openai/gpt-6-astra",
				Description: "provider/model for every spawn."},
			{Key: "--agent", Kind: provider.CatalogString, Placeholder: "build",
				Description: "opencode agent to run."},
			{Key: "--variant", Kind: provider.CatalogString, Placeholder: "high",
				Description: "Model variant (reasoning effort)."},
		},
	})
}
