package config

// GeneralConfig holds top-level Agents knobs. Reflected into the
// configs table via pkg/entity.StructToConfigs (Owner = "agents-general"
// at registration time). See agents-design.md §8.1.
type GeneralConfig struct {
	Enabled         bool   `wick:"checkbox;desc=Enable the Agents feature."`
	MaxConcurrent   int    `wick:"number;desc=Max concurrent agent subprocesses. Default: 2."`
	IdleTimeoutSec  int    `wick:"number;desc=Seconds of inactivity before subprocess is killed. Default: 120."`
	DefaultProvider string `wick:"dropdown=claude|codex|gemini;desc=Default CLI provider."`
	GateEnabled     bool   `wick:"checkbox;desc=Route every Bash command through wick-gate. When off, the provider's own default permission handling applies (claude headless: blocks; others vary)."`
	AllowedCmds     string `wick:"kvlist;desc=Allowed shell command patterns. Unlisted commands are auto-blocked."`
	PublicURL       string `wick:"url;desc=Public base URL of this wick instance. Used for the dashboard meta-command."`
}

// DefaultGeneralConfig returns the seed values used when the configs
// table has no row for a given key. Defaults come from this function
// rather than `default=` tags because the kvlist widget does not honor
// `default=`.
func DefaultGeneralConfig() GeneralConfig {
	return GeneralConfig{
		Enabled:         false,
		MaxConcurrent:   2,
		IdleTimeoutSec:  120,
		DefaultProvider: "claude",
		GateEnabled:     false,
		AllowedCmds:     `[{"value":"git status"},{"value":"git diff"},{"value":"git log"},{"value":"ls *"},{"value":"cat *"}]`,
	}
}
