package mcpconfig

// WickEnvVars is the canonical list of environment variables wick reads
// at runtime. Used when installing wick as an MCP server into clients
// that require an explicit allowlist for env passthrough (currently:
// Codex CLI — see openai/codex#3064).
//
// MAINTENANCE: when you add a new os.Getenv("FOO") (or `env:"FOO"`
// struct tag) somewhere in wick, add "FOO" here so installs forward
// it to the spawned MCP child. Build-time-only env (GOOS, GOARCH,
// RELEASE_*) and platform defaults (HOME, PATH, …) don't belong here.
//
// Empty entries at install time are harmless — Codex silently skips
// any name whose value is empty in the user's shell. Listing many
// keys preemptively is therefore safe.
var WickEnvVars = []string{
	// Database connection (DSN).
	"DATABASE_URL",
	// Encrypted-fields layer.
	"WICK_ENC_KEY",
	"WICK_ENC_DISABLE",
	// App identity / branding / first-boot seed.
	"APP_NAME",
	"APP_URL",
	"APP_ADMIN_EMAILS",
	"APP_ADMIN_PASSWORD",
	// HTTP server (irrelevant in stdio MCP mode but harmless to pass).
	"PORT",
	// Agent gate binary path (dev override).
	"WICK_GATE_BIN",
	// Tray-mode flag.
	"WICK_TRAY",
	// Claude provider log overrides (dev).
	"WICK_CLAUDE_STDOUT_LOG",
	"WICK_CLAUDE_STDERR_LOG",
}
