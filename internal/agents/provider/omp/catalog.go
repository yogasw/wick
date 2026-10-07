package omp

import "github.com/yogasw/wick/internal/agents/provider"

// init registers omp's env + args picker catalog. Sourced from oh-my-pi
// docs/cli-reference.md and docs/environment-variables.md (fc671eb).
// --profile is deliberately absent: the instance owns it (accounts.go).
func init() {
	provider.RegisterCatalog(provider.TypeOMP, provider.ProviderCatalog{
		Env: []provider.CatalogEntry{
			{Key: "OMP_MCP_TIMEOUT_MS", Kind: provider.CatalogInt, Placeholder: "30000",
				Description: "Print mode: how long to wait for MCP servers before the first turn (0 = forever)."},
			{Key: "OMP_MCP_REQUIRE_READY", Kind: provider.CatalogBool, Options: []string{"1", "0"},
				Description: "Exit 1 instead of running when an MCP server is not ready."},
			{Key: "PI_SMOL_MODEL", Kind: provider.CatalogString, Placeholder: "provider/model",
				Description: "Fast model for lightweight tasks."},
			{Key: "PI_SLOW_MODEL", Kind: provider.CatalogString, Placeholder: "provider/model",
				Description: "Reasoning model for thorough analysis."},
			{Key: "PI_CONFIG_DIR", Kind: provider.CatalogString, Placeholder: ".omp",
				Description: "Config root relative to $HOME (default .omp)."},
		},
		Args: []provider.CatalogEntry{
			{Key: "--model", Kind: provider.CatalogString, Placeholder: "openai-codex/gpt-5.2",
				Description: "Model or role for every spawn."},
			{Key: "--thinking", Kind: provider.CatalogEnum,
				Options:     []string{"off", "minimal", "low", "medium", "high", "xhigh", "max", "auto"},
				Description: "Thinking level."},
			{Key: "--max-time", Kind: provider.CatalogString, Placeholder: "30m",
				Description: "Wall-clock cap for one run (s/m/h)."},
			{Key: "--no-lsp", Kind: provider.CatalogBool, Options: []string{"true", "false"},
				Description: "Disable LSP integration."},
		},
	})
}
