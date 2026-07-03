package gemini

import "github.com/yogasw/wick/internal/agents/provider"

// init registers Gemini CLI's env + args picker catalog with the parent
// provider package. Sourced from github.com/google-gemini/gemini-cli
// docs/reference/configuration.md (Environment variables + Command-line
// arguments). Broad on purpose — the UI dropdown lets the operator search.
func init() {
	provider.RegisterCatalog(provider.TypeGemini, provider.ProviderCatalog{
		Env: []provider.CatalogEntry{
			// Auth & API
			{Key: "GEMINI_API_KEY", Kind: provider.CatalogString, Placeholder: "AIza...",
				Description: "API key for the Gemini API (stored masked)."},
			{Key: "GOOGLE_API_KEY", Kind: provider.CatalogString, Placeholder: "AIza...",
				Description: "Google Cloud API key — Vertex AI express mode (stored masked)."},
			{Key: "GOOGLE_APPLICATION_CREDENTIALS", Kind: provider.CatalogString, Placeholder: "/path/to/creds.json",
				Description: "Path to a Google service-account JSON file."},
			{Key: "GOOGLE_CLOUD_PROJECT", Kind: provider.CatalogString, Placeholder: "my-gcp-project",
				Description: "GCP project ID (required for Code Assist / Vertex AI)."},

			// Model & API config
			{Key: "GEMINI_MODEL", Kind: provider.CatalogString, Placeholder: "gemini-3-flash-preview",
				Description: "Default Gemini model for this instance."},
			{Key: "GOOGLE_GENAI_USE_VERTEXAI", Kind: provider.CatalogBool, Options: []string{"true", "false"},
				Description: "Route requests through Vertex AI instead of the Gemini API."},
			{Key: "GOOGLE_GENAI_API_VERSION", Kind: provider.CatalogString, Placeholder: "v1",
				Description: "API version for Gemini API requests."},
			{Key: "GOOGLE_GEMINI_BASE_URL", Kind: provider.CatalogString, Placeholder: "https://...",
				Description: "Custom base URL for the Gemini API (HTTPS or localhost)."},
			{Key: "GOOGLE_VERTEX_BASE_URL", Kind: provider.CatalogString, Placeholder: "https://...",
				Description: "Custom base URL for the Vertex AI API (HTTPS or localhost)."},

			// Sandbox & trust
			{Key: "GEMINI_SANDBOX", Kind: provider.CatalogBool, Options: []string{"true", "false"},
				Description: "Enable sandboxing for tool execution."},
			{Key: "GEMINI_CLI_TRUST_WORKSPACE", Kind: provider.CatalogBool, Options: []string{"true", "false"},
				Description: "Trust the current workspace (bypass the folder-trust check)."},
			{Key: "GEMINI_CLI_TRUSTED_FOLDERS_PATH", Kind: provider.CatalogString, Placeholder: "/path/to/trustedFolders.json",
				Description: "Custom location for trustedFolders.json."},

			// CLI paths & identity
			{Key: "GEMINI_CLI_HOME", Kind: provider.CatalogString, Placeholder: "/abs/path",
				Description: "Root directory for user-level config/storage."},
			{Key: "GEMINI_CLI_SURFACE", Kind: provider.CatalogString, Placeholder: "label",
				Description: "Custom User-Agent label for API tracking."},
			{Key: "GEMINI_CLI_SYSTEM_DEFAULTS_PATH", Kind: provider.CatalogString, Placeholder: "/path",
				Description: "Override the system-defaults file location."},
			{Key: "GEMINI_CLI_SYSTEM_SETTINGS_PATH", Kind: provider.CatalogString, Placeholder: "/path",
				Description: "Override the system-settings file location."},
			{Key: "GEMINI_CLI_IDE_PID", Kind: provider.CatalogInt, Placeholder: "12345",
				Description: "IDE process PID for integration."},

			// Telemetry
			{Key: "GEMINI_TELEMETRY_ENABLED", Kind: provider.CatalogBool, Options: []string{"false", "true"},
				Description: "Enable telemetry collection."},
			{Key: "GEMINI_TELEMETRY_TRACES_ENABLED", Kind: provider.CatalogBool, Options: []string{"false", "true"},
				Description: "Enable detailed trace collection."},
			{Key: "GEMINI_TELEMETRY_TARGET", Kind: provider.CatalogEnum, Options: []string{"local", "gcp"},
				Description: "Telemetry destination."},
			{Key: "GEMINI_TELEMETRY_OTLP_ENDPOINT", Kind: provider.CatalogString, Placeholder: "https://...",
				Description: "OTLP endpoint address."},
			{Key: "GEMINI_TELEMETRY_OTLP_PROTOCOL", Kind: provider.CatalogEnum, Options: []string{"grpc", "http"},
				Description: "OTLP communication protocol."},
			{Key: "OTLP_GOOGLE_CLOUD_PROJECT", Kind: provider.CatalogString, Placeholder: "my-gcp-project",
				Description: "GCP project ID for telemetry."},
		},
		Args: []provider.CatalogEntry{
			{Key: "--model", Kind: provider.CatalogString, Placeholder: "gemini-3-flash-preview",
				Description: "Model to run with for every spawn."},
			{Key: "--approval-mode", Kind: provider.CatalogEnum, Options: []string{"default", "auto_edit", "plan", "yolo"},
				Description: "Approval mode for actions."},
			{Key: "--yolo", Kind: provider.CatalogBool, Options: []string{"true", "false"},
				Description: "Auto-approve all actions (no confirmation prompts)."},
		},
	})
}
