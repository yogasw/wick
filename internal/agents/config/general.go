package config

import systemprompt "github.com/yogasw/wick/internal/agents/system-prompt"

// GeneralConfig holds top-level Agents knobs. Reflected into the
// configs table via pkg/entity.StructToConfigs (Owner = "agents" at
// registration time). See agents-design.md §8.1.
// Gate settings live in GateConfig; channel settings in SlackChannelConfig.
type GeneralConfig struct {
	Enabled                   bool   `wick:"bool;group=General|Top-level Agents switches and defaults.;desc=Enable the Agents feature."`
	DefaultProvider           string `wick:"dropdown=claude|codex|gemini;group=General;desc=Default CLI provider."`
	PublicURL                 string `wick:"url;group=General;desc=Public base URL of this wick instance. Used for the dashboard meta-command."`
	MaxConcurrent             int    `wick:"number;group=Concurrency & Lifecycle|How many agent subprocesses run at once and when idle ones are reclaimed.;desc=Max concurrent agent subprocesses across all providers. 0 = unlimited. Default: 2."`
	IdleTimeoutSec            int    `wick:"number;group=Concurrency & Lifecycle;desc=Seconds of inactivity before subprocess is killed. Default: 120."`
	KillAfterIdleSec          int    `wick:"number;group=Concurrency & Lifecycle;desc=Extra seconds after idle timeout before the subprocess is killed. 0 = kill immediately at idle timeout. Default: 0."`
	PreemptIdle               bool   `wick:"bool;group=Concurrency & Lifecycle;desc=When the pool is full and a new session is queued, preempt the longest-idle active subprocess to free its slot. Killed sessions resume via --resume on their next message."`
	AutoRescan                bool   `wick:"bool;group=Concurrency & Lifecycle;desc=Auto re-probe provider binaries when cached version is older than 24h. Off = refresh only via Rescan button."`
	SystemPrompt              string `wick:"textarea;desc=Global interaction rules appended to every preset's system prompt on spawn. Cannot replace the preset — only adds to it. Use for org-wide guardrails, prompt-injection defenses, or shared conventions every agent must follow."`
	WorkflowGuardMode         string `wick:"dropdown=off|warn|block;group=Workflow|Workflow guard policy, parallelism, and run-event export.;desc=Workflow guard policy. off = skip guard entirely (default). warn = log violations, allow run. block = reject Publish/Run on violations."`
	WorkflowMaxParallelGlobal int    `wick:"number;group=Workflow;desc=Global parallel cap. 0 = parallel disabled, all workflows serial (default). N > 0 = parallel enabled; at most N runs execute simultaneously across all workflows. Per-workflow concurrency.max is honoured as an inner cap."`
	WorkflowLokiURL           string `wick:"url;group=Workflow;desc=Loki push endpoint for workflow run events (e.g. http://loki:3100). Empty = disabled."`
	WorkflowLokiLabels        string `wick:"text;group=Workflow;desc=Extra Loki stream labels as comma-separated key=value pairs (e.g. env=prod,team=eng)."`
	MCPUninstalledClients     string `wick:"hidden;desc=Comma-separated MCP client IDs the user has manually uninstalled. Managed by the UI — do not edit by hand."`
	Router9Enabled            bool   `wick:"bool;group=9router|Embedded 9router lifecycle. Access is managed at /admin/tools.;desc=Master switch for the embedded 9router. Off = dashboard, the /9router/v1 API proxy, autostart, and all controls are disabled."`
	Router9Autostart          bool   `wick:"bool;group=9router;desc=Auto-start the embedded 9router process on boot (only when the master switch is on)."`
	Router9ExternalAPI        bool   `wick:"bool;group=9router;desc=Allow the /9router/v1 API to be reached from outside this machine (via a tunnel or the public URL). Off (default) = the API answers only local spawns; remote callers get 403. On = remote callers are forwarded to 9router with their real client address, so 9router enforces its own API key for non-local traffic (local spawns still need no key)."`
	TraceEventInlineKB        int    `wick:"number;group=Tracing|Limits on how trace-event payloads are stored on disk.;desc=Max KB for a trace event payload stored inline in the turn index. Events larger than this are written to a separate file and loaded on demand. Default: 10."`
	TraceEventMaxKB           int    `wick:"number;group=Tracing;desc=Hard cap in KB for a single trace event payload file. Payloads exceeding this are truncated before write. 0 = no cap. Default: 512."`
	AdminSeeAll               bool   `wick:"bool;group=Access|Visibility scope for admins.;desc=When on, admins see every project and every session (legacy behaviour). When off (default), admins are scoped like regular users: only projects granted via tags plus their own unscoped sessions. Ownerless sessions (no creator) are hidden from everyone while off."`
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
		SystemPrompt:       systemprompt.DefaultSystemPrompt(),
		WorkflowGuardMode:  "off",
		TraceEventInlineKB: 10,
		TraceEventMaxKB:    512,
		Router9Enabled:     true,
	}
}
