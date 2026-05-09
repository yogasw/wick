package config

// GeneralConfig holds top-level Agents knobs. Reflected into the
// configs table via pkg/entity.StructToConfigs (Owner = "agents-general"
// at registration time). See agents-design.md §8.1.
type GeneralConfig struct {
	Enabled         bool   `wick:"checkbox;desc=Enable the Agents feature."`
	MaxConcurrent   int    `wick:"number;desc=Max concurrent agent subprocesses. Default: 2."`
	IdleTimeoutSec  int    `wick:"number;desc=Seconds of inactivity before subprocess is killed. Default: 120."`
	DefaultProvider string `wick:"dropdown=claude|codex|gemini;desc=Default CLI provider."`
	GateEnabled     bool   `wick:"checkbox;desc=Enable command gate (wick-gate). When on, only patterns in the list below are allowed; everything else is auto-blocked."`
	AllowedCmds     string `wick:"kvlist=pattern|scope;desc=Command whitelist. pattern supports a trailing * wildcard (e.g. 'git *'). scope (optional) restricts path args to a directory prefix."`
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
		AllowedCmds:     `[{"pattern":"git status"},{"pattern":"git diff *"},{"pattern":"git log *"},{"pattern":"ls *"},{"pattern":"cat *"}]`,
	}
}
