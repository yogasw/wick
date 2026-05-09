package config

// GeneralConfig holds top-level Agents knobs. Reflected into the
// configs table via pkg/entity.StructToConfigs (Owner = "agents-general"
// at registration time). See agents-design.md §8.1.
type GeneralConfig struct {
	Enabled           bool   `wick:"checkbox;desc=Enable the Agents feature."`
	MaxConcurrent     int    `wick:"number;desc=Max concurrent agent subprocesses. Default: 2."`
	IdleTimeoutSec    int    `wick:"number;desc=Seconds of inactivity before subprocess is killed. Default: 120."`
	DefaultProvider   string `wick:"dropdown=claude|codex|gemini;desc=Default CLI provider."`
	BypassPermissions bool   `wick:"checkbox;desc=Always pass --permission-mode bypassPermissions to Claude. Enable this if Claude prompts for permission in Slack/HTTP sessions and no gate is configured."`
	KillAfterIdleSec  int    `wick:"number;desc=Extra seconds after idle timeout before the subprocess is killed. 0 = kill immediately at idle timeout. Default: 0."`
	GateEnabled       bool   `wick:"checkbox;desc=Route every Bash command through the gate sidecar. When off, the provider's own default permission handling applies (claude headless: blocks; others vary)."`
	AllowedCmds       string `wick:"kvlist=pattern|scope;desc=Command whitelist. pattern supports a trailing * wildcard (e.g. 'git *'). scope (optional) restricts path args to a directory prefix."`
	PublicURL         string `wick:"url;desc=Public base URL of this wick instance. Used for the dashboard meta-command."`
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
