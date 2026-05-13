package config

// GeneralConfig holds top-level Agents knobs. Reflected into the
// configs table via pkg/entity.StructToConfigs (Owner = "agents" at
// registration time). See agents-design.md §8.1.
// Gate settings live in GateConfig; channel settings in SlackChannelConfig.
type GeneralConfig struct {
	Enabled           bool   `wick:"checkbox;desc=Enable the Agents feature."`
	MaxConcurrent     int    `wick:"number;desc=Max concurrent agent subprocesses. Default: 2."`
	IdleTimeoutSec    int    `wick:"number;desc=Seconds of inactivity before subprocess is killed. Default: 120."`
	DefaultProvider   string `wick:"dropdown=claude|codex|gemini;desc=Default CLI provider."`
	BypassPermissions bool   `wick:"checkbox;desc=Always pass --permission-mode bypassPermissions to Claude. Enable this if Claude prompts for permission in Slack/HTTP sessions and no gate is configured."`
	KillAfterIdleSec  int    `wick:"number;desc=Extra seconds after idle timeout before the subprocess is killed. 0 = kill immediately at idle timeout. Default: 0."`
	PublicURL         string `wick:"url;desc=Public base URL of this wick instance. Used for the dashboard meta-command."`
	AutoRescan        bool   `wick:"checkbox;desc=Auto re-probe provider binaries when cached version is older than 24h. Off = refresh only via Rescan button."`
	PreemptIdle       bool   `wick:"checkbox;desc=When the pool is full and a new session is queued, preempt the longest-idle active subprocess to free its slot. Killed sessions resume via --resume on their next message."`
	SystemPrompt string `wick:"textarea;desc=Global interaction rules appended to every preset's system prompt on spawn. Cannot replace the preset — only adds to it. Use for org-wide guardrails, prompt-injection defenses, or shared conventions every agent must follow."`
}

// DefaultGeneralConfig returns the seed values used when the configs
// table has no row for a given key.
func DefaultGeneralConfig() GeneralConfig {
	return GeneralConfig{
		Enabled:            false,
		MaxConcurrent:      2,
		IdleTimeoutSec:     120,
		DefaultProvider:    "claude",
		AutoRescan:         true,
		PreemptIdle:        true,
		SystemPrompt:    DefaultSystemPrompt(),
	}
}
