package codex

import "github.com/yogasw/wick/internal/agents/provider"

// init registers Codex's env + args picker catalog with the parent
// provider package. Codex exposes only a few env vars; most behaviour is
// configured via config.toml keys, which the CLI accepts inline as
// `-c key=value` — those are surfaced here as args. Sources:
// developers.openai.com/codex/environment-variables , .../config-reference ,
// .../cli/reference. Broad on purpose.
func init() {
	provider.RegisterCatalog(provider.TypeCodex, provider.ProviderCatalog{
		Env: []provider.CatalogEntry{
			{Key: "CODEX_HOME", Kind: provider.CatalogString, Placeholder: "/abs/path/to/.codex",
				Description: "Root dir for Codex state (config, auth, logs) — isolates this instance."},
			{Key: "CODEX_SQLITE_HOME", Kind: provider.CatalogString, Placeholder: "/abs/path",
				Description: "Location for SQLite-backed state storage."},
			{Key: "CODEX_API_KEY", Kind: provider.CatalogString, Placeholder: "sk-...",
				Description: "API key for non-interactive runs (stored masked)."},
			{Key: "CODEX_ACCESS_TOKEN", Kind: provider.CatalogString, Placeholder: "token",
				Description: "ChatGPT/Codex access token for trusted automation (stored masked)."},
			{Key: "CODEX_NON_INTERACTIVE", Kind: provider.CatalogBool, Options: []string{"1", "0"},
				Description: "Skip interactive installer/runtime prompts."},
			{Key: "CODEX_CA_CERTIFICATE", Kind: provider.CatalogString, Placeholder: "/path/to/ca.pem",
				Description: "PEM CA bundle for TLS-interception environments."},
			{Key: "SSL_CERT_FILE", Kind: provider.CatalogString, Placeholder: "/path/to/ca.pem",
				Description: "Fallback PEM CA bundle path."},
			{Key: "RUST_LOG", Kind: provider.CatalogEnum, Options: []string{"error", "warn", "info", "debug", "trace"},
				Description: "Log verbosity filter."},
		},
		Args: []provider.CatalogEntry{
			// Native flags
			{Key: "--model", Kind: provider.CatalogString, Placeholder: "gpt-5.5",
				Description: "Override the configured model."},
			{Key: "--sandbox", Kind: provider.CatalogEnum, Options: []string{"read-only", "workspace-write", "danger-full-access"},
				Description: "Sandbox policy for shell commands."},
			{Key: "--ask-for-approval", Kind: provider.CatalogEnum, Options: []string{"untrusted", "on-request", "never"},
				Description: "When Codex pauses for human approval."},
			{Key: "--add-dir", Kind: provider.CatalogString, Placeholder: "/abs/path",
				Description: "Grant write access to an additional directory."},
			{Key: "--profile", Kind: provider.CatalogString, Placeholder: "profile-name",
				Description: "Layer an additional config profile."},
			{Key: "--search", Kind: provider.CatalogBool, Options: []string{"true", "false"},
				Description: "Enable live web search instead of cached mode."},
			{Key: "--oss", Kind: provider.CatalogBool, Options: []string{"true", "false"},
				Description: "Use a local open-source provider (requires Ollama)."},

			// config.toml keys via -c key=value
			{Key: "-c model_reasoning_effort=", Kind: provider.CatalogEnum, Options: []string{"minimal", "low", "medium", "high", "xhigh"},
				Description: "Reasoning effort for supported models."},
			{Key: "-c model_reasoning_summary=", Kind: provider.CatalogEnum, Options: []string{"auto", "concise", "detailed", "none"},
				Description: "Reasoning summary detail level."},
			{Key: "-c model_verbosity=", Kind: provider.CatalogEnum, Options: []string{"low", "medium", "high"},
				Description: "GPT-5 Responses API verbosity override."},
			{Key: "-c sandbox_mode=", Kind: provider.CatalogEnum, Options: []string{"read-only", "workspace-write", "danger-full-access"},
				Description: "Filesystem/network access policy during execution."},
			{Key: "-c approval_policy=", Kind: provider.CatalogEnum, Options: []string{"untrusted", "on-request", "never"},
				Description: "When Codex pauses for command approval."},
			{Key: "-c web_search=", Kind: provider.CatalogEnum, Options: []string{"cached", "live", "disabled"},
				Description: "Web search mode."},
			{Key: "-c model_context_window=", Kind: provider.CatalogInt, Placeholder: "256000",
				Description: "Context-window tokens for the active model."},
			{Key: "-c model_auto_compact_token_limit=", Kind: provider.CatalogInt, Placeholder: "200000",
				Description: "Token threshold that triggers automatic history compaction."},
			{Key: "-c personality=", Kind: provider.CatalogEnum, Options: []string{"none", "friendly", "pragmatic"},
				Description: "Default communication style for supporting models."},
			{Key: "-c service_tier=", Kind: provider.CatalogEnum, Options: []string{"flex", "fast"},
				Description: "Preferred service tier for new turns."},
			{Key: "-c history.persistence=", Kind: provider.CatalogEnum, Options: []string{"save-all", "none"},
				Description: "Whether to save session transcripts to history.jsonl."},
			{Key: "-c hide_agent_reasoning=", Kind: provider.CatalogBool, Options: []string{"true", "false"},
				Description: "Suppress reasoning events in TUI/exec output."},
			{Key: "-c model_provider=", Kind: provider.CatalogString, Placeholder: "openai",
				Description: "Provider ID from the model_providers table."},
			{Key: "-c model_instructions_file=", Kind: provider.CatalogString, Placeholder: "/path/to/instructions.md",
				Description: "Custom instructions file instead of AGENTS.md."},
		},
	})
}
