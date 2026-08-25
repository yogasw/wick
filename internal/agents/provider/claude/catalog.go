package claude

import "github.com/yogasw/wick/internal/agents/provider"

// init registers Claude Code's env + args picker catalog with the parent
// provider package. Sourced from https://code.claude.com/docs/en/settings
// (Environment variables) and the Claude Code CLI reference. Broad on
// purpose — the UI dropdown lets the operator search.
func init() {
	provider.RegisterCatalog(provider.TypeClaude, provider.ProviderCatalog{
		Env: []provider.CatalogEntry{
			// Auth & API
			{Key: "ANTHROPIC_API_KEY", Kind: provider.CatalogString, Placeholder: "sk-ant-...",
				Description: "API key for direct Anthropic API auth (stored masked)."},
			{Key: "ANTHROPIC_AUTH_TOKEN", Kind: provider.CatalogString, Placeholder: "token",
				Description: "Auth token for Claude.ai login (stored masked)."},
			{Key: "ANTHROPIC_MODEL", Kind: provider.CatalogString, Placeholder: "claude-opus-4-8",
				Description: "Default model ID for this instance."},

			// Model & thinking
			{Key: "MAX_THINKING_TOKENS", Kind: provider.CatalogInt, Placeholder: "0",
				Description: "Max extended-thinking tokens; 0 disables thinking."},
			{Key: "CLAUDE_CODE_EFFORT_LEVEL", Kind: provider.CatalogEnum, Options: []string{"low", "medium", "high", "xhigh"},
				Description: "Default effort level per session."},

			// Config & paths
			{Key: "CLAUDE_CONFIG_DIR", Kind: provider.CatalogString, Placeholder: "/abs/path/to/config-dir",
				Description: "Custom config directory — isolates this instance's settings/credentials."},

			// Feature toggles (1 = disabled unless noted)
			{Key: "DISABLE_AUTOUPDATER", Kind: provider.CatalogBool, Options: []string{"1", "0"},
				Description: "Disable Claude Code auto-updates entirely."},
			{Key: "DISABLE_AUTO_COMPACT", Kind: provider.CatalogBool, Options: []string{"1", "0"},
				Description: "Disable automatic context compaction."},
			{Key: "CLAUDE_CODE_DISABLE_AUTO_MEMORY", Kind: provider.CatalogBool, Options: []string{"1", "0"},
				Description: "Disable the auto-memory feature."},
			{Key: "CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING", Kind: provider.CatalogBool, Options: []string{"1", "0"},
				Description: "Disable file checkpointing / rewind."},
			{Key: "CLAUDE_CODE_DISABLE_ARTIFACT", Kind: provider.CatalogBool, Options: []string{"1", "0"},
				Description: "Disable the Artifact tool."},
			{Key: "CLAUDE_CODE_DISABLE_BUNDLED_SKILLS", Kind: provider.CatalogBool, Options: []string{"1", "0"},
				Description: "Disable bundled skills and workflows."},
			{Key: "CLAUDE_CODE_DISABLE_AGENT_VIEW", Kind: provider.CatalogBool, Options: []string{"1", "0"},
				Description: "Disable background agents and the agent view."},
			{Key: "CLAUDE_CODE_DISABLE_WORKFLOWS", Kind: provider.CatalogBool, Options: []string{"1", "0"},
				Description: "Disable dynamic workflows."},
			{Key: "CLAUDE_CODE_DISABLE_FEEDBACK_SURVEY", Kind: provider.CatalogBool, Options: []string{"1", "0"},
				Description: "Suppress the session-quality survey."},
			{Key: "CLAUDE_CODE_SKIP_PROMPT_HISTORY", Kind: provider.CatalogBool, Options: []string{"1", "0"},
				Description: "Disable transcript writes."},
			{Key: "CLAUDE_CODE_ENABLE_TELEMETRY", Kind: provider.CatalogBool, Options: []string{"0", "1"},
				Description: "Enable OpenTelemetry metrics collection."},
			{Key: "CLAUDE_CODE_ENABLE_AWAY_SUMMARY", Kind: provider.CatalogBool, Options: []string{"0", "1"},
				Description: "Show a session recap when returning after an absence."},
			{Key: "CLAUDE_CODE_USE_POWERSHELL_TOOL", Kind: provider.CatalogBool, Options: []string{"1", "0"},
				Description: "Enable the PowerShell tool on Windows."},

			// Rendering & accessibility
			{Key: "NO_COLOR", Kind: provider.CatalogBool, Options: []string{"1", "0"},
				Description: "Disable colored output."},
			{Key: "FORCE_COLOR", Kind: provider.CatalogBool, Options: []string{"1", "0"},
				Description: "Force colored output."},
			{Key: "CLAUDE_AX_SCREEN_READER", Kind: provider.CatalogBool, Options: []string{"1", "0"},
				Description: "Enable screen-reader-friendly output."},

			// Helpers & telemetry
			{Key: "CLAUDE_CODE_API_KEY_HELPER_TTL_MS", Kind: provider.CatalogInt, Placeholder: "3600000",
				Description: "Refresh interval (ms) for the apiKeyHelper."},
			{Key: "CLAUDE_CODE_OTEL_HEADERS_HELPER_DEBOUNCE_MS", Kind: provider.CatalogInt, Placeholder: "5000",
				Description: "Refresh interval (ms) for the otelHeadersHelper."},
			{Key: "OTEL_METRICS_EXPORTER", Kind: provider.CatalogString, Placeholder: "otlp",
				Description: "OpenTelemetry metrics exporter."},
		},
		Args: []provider.CatalogEntry{
			{Key: "--model", Kind: provider.CatalogString, Placeholder: "claude-opus-4-8",
				Description: "Model to run with for every spawn."},
			{Key: "--permission-mode", Kind: provider.CatalogEnum, Options: []string{"default", "acceptEdits", "plan", "bypassPermissions"},
				Description: "Permission mode passed to every spawn."},
		},
	})
}
