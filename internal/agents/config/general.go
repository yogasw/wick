package config

import systemprompt "github.com/yogasw/wick/internal/agents/system-prompt"

// GeneralConfig holds top-level Agents knobs. Reflected into the
// configs table via pkg/entity.StructToConfigs (Owner = "agents" at
// registration time). See agents-design.md §8.1.
// Gate settings live in GateConfig; channel settings in SlackChannelConfig.
type GeneralConfig struct {
	Enabled                   bool   `wick:"bool;group=General|Top-level Agents switches and defaults.;desc=Enable the Agents feature."`
	DefaultProvider           string `wick:"dropdown;key=default_provider;group=General;desc=Default provider instance for new sessions when none is picked (channels, API, quick-create). Options are your configured provider instances (type or type/name); empty falls back to claude."`
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
	AirouterEnabled           bool   `wick:"bool;group=AI Router|Embedded AI-router lifecycle (9router, OmniRoute, …). Access is managed at /admin/tools; per-router autostart + external-API toggles live on the AI Router page.;desc=Master switch for the embedded AI routers. Off = every dashboard, the /airouter/<id>/v1 API proxies, autostart, and all controls are disabled."`
	TraceEventInlineKB        int    `wick:"number;group=Tracing|Limits on how trace-event payloads are stored on disk.;desc=Max KB for a trace event payload stored inline in the turn index. Events larger than this are written to a separate file and loaded on demand. Default: 10."`
	TraceEventMaxKB           int    `wick:"number;group=Tracing;desc=Hard cap in KB for a single trace event payload file. Payloads exceeding this are truncated before write. 0 = no cap. Default: 512."`
	AdminSeeAll               bool   `wick:"bool;group=Access|Visibility scope for admins.;desc=When on, admins see every project and every session (legacy behaviour). When off (default), admins are scoped like regular users: only projects granted via tags plus their own unscoped sessions. Ownerless sessions (no creator) are hidden from everyone while off."`

	// Sub-agent governor. These are SYSTEM-WIDE CEILINGS, not per-role
	// defaults: an agent profile can lower them but never raise them.
	// Role-level settings (provider, model, prompt, tool tags) live on
	// the profile itself, at /manager/agents/profiles.
	SubAgentsEnabled     bool `wick:"bool;group=Sub-agents|System-wide ceilings for sub-agent delegation. Individual roles are configured under Agent Profiles; these values cap every role and cannot be raised by one.;desc=Master switch for sub-agent delegation. Off = the wick_delegate and wick_agents tools disappear entirely and no sub-agent can be spawned. Use as an emergency stop or for a staged rollout."`
	SubAgentsMaxDepth    int  `wick:"number;group=Sub-agents;desc=How many levels deep delegation may nest (a sub-agent delegating again). Guards against runaway recursion. Default: 3."`
	SubAgentsRootBudget  int  `wick:"number;group=Sub-agents;desc=Total agentic turns one delegation tree may consume across every sub-agent in it. When exhausted, running sub-agents finish but no new ones start. Default: 40."`
	SubAgentsMaxParallel int  `wick:"number;group=Sub-agents;desc=Max sub-agents running concurrently within one conversation. 1 (default) runs them one at a time in a visible queue; raise it for parallel throughput at the cost of interleaved output and faster budget burn."`
	SubAgentsMaxTurns    int  `wick:"number;group=Sub-agents;desc=Hard ceiling on turns for any single sub-agent. Both the profile default and a caller's request are clamped to this. Default: 50."`
	// Token ceilings. Turn limits bound how MANY times a sub-agent runs;
	// they do not bound what each run costs — one turn that reads a large
	// file can cost more than ten small ones. 0 disables a ceiling, which
	// is also what happens in practice for providers that report no usage.
	SubAgentsMaxTokens     int `wick:"number;group=Sub-agents;desc=Hard ceiling on tokens any single sub-agent may spend. 0 = no per-sub-agent token cap (turn limits still apply). Only enforceable for providers that report usage. Default: 200000."`
	SubAgentsRootTokens    int `wick:"number;group=Sub-agents;desc=Total tokens one delegation tree may spend across every sub-agent in it. When exhausted, running sub-agents finish but no new ones start. 0 = no token budget. Default: 1000000."`
	SubAgentsStaleClaimMin int `wick:"number;group=Sub-agents;desc=Minutes before a task-board claim held by a vanished worker is released back to the queue. Without this a crashed worker pins its task forever. Default: 30."`
	// Agent-to-agent messaging. Turn and token budgets bound a tree's
	// total work; none of them bounds two agents trading short messages,
	// which is cheap per message and unbounded in count.
	SubAgentsMaxHops       int `wick:"number;group=Sub-agents;desc=How many messages agents may exchange with each other between human turns. Guards against two agents talking in a loop. Reset whenever a person sends a message; agents cannot reset it themselves. Default: 10."`
	SubAgentsAskTimeoutMin int `wick:"number;group=Sub-agents;desc=Minutes an agent waits for an answer to a blocking ask before giving up. The question stays in the recipient's inbox either way. Default: 10."`
	SubAgentsInboxCap      int `wick:"number;group=Sub-agents;desc=How many undelivered messages one agent may have waiting before senders are refused. Stops a fast agent from burying a slow one under work it will never read. Default: 20."`

	SubAgentsMentionRouter bool `wick:"bool;group=Sub-agents;desc=Act on @name at the start of a line. An @handle messages that agent, an @role starts one. Off = mentions stay plain text and only the delegate operation spawns anything. Default: on."`

	SubAgentsMaxIterations    int    `wick:"number;group=Sub-agents;desc=Checker rounds one investigation may run before it stops and escalates to a human. Default: 5."`
	SubAgentsMaxRuntimeMin    int    `wick:"number;group=Sub-agents;desc=Minutes one investigation may run before it stops, keeps whatever it has, and escalates. Catches an agent that is slow rather than chatty, which turn limits do not. Default: 20."`
	SubAgentsMinConfidence    string `wick:"group=Sub-agents;desc=Minimum checker confidence before a customer-facing draft may be produced: low, medium, or high. Default: medium."`
	SubAgentsNoEvidenceRounds int    `wick:"number;group=Sub-agents;desc=Consecutive rounds that add no new evidence before an investigation stops. 1 would abandon a run that came up empty once and would have landed the next round. Default: 2."`
}

// DefaultGeneralConfig returns the seed values used when the configs
// table has no row for a given key.
func DefaultGeneralConfig() GeneralConfig {
	return GeneralConfig{
		Enabled:        false,
		MaxConcurrent:  2,
		IdleTimeoutSec: 120,
		// DefaultProvider is a picker (JSON [{id,name}]); empty = fall back
		// to claude at spawn. Not seeded with a value so a fresh install
		// doesn't pin a provider the operator never chose.
		AutoRescan:         true,
		PreemptIdle:        true,
		SystemPrompt:       systemprompt.DefaultSystemPrompt(),
		WorkflowGuardMode:  "off",
		TraceEventInlineKB: 10,
		TraceEventMaxKB:    512,
		AirouterEnabled:    true,
		// Sub-agents ship OFF: delegation spawns real processes and spends
		// real tokens, so it is opt-in rather than something a fresh
		// install discovers by surprise. The ceilings below apply the
		// moment it is switched on.
		SubAgentsEnabled:          false,
		SubAgentsMaxDepth:         3,
		SubAgentsRootBudget:       40,
		SubAgentsMaxParallel:      1,
		SubAgentsMaxTurns:         50,
		SubAgentsMaxTokens:        200_000,
		SubAgentsRootTokens:       1_000_000,
		SubAgentsStaleClaimMin:    30,
		SubAgentsMaxHops:          delegationDefaultMaxHops,
		SubAgentsAskTimeoutMin:    10,
		SubAgentsInboxCap:         20,
		SubAgentsMentionRouter:    true,
		SubAgentsMaxIterations:    delegationDefaultMaxIterations,
		SubAgentsMaxRuntimeMin:    delegationDefaultMaxRuntimeMin,
		SubAgentsMinConfidence:    "medium",
		SubAgentsNoEvidenceRounds: delegationDefaultNoEvidenceRounds,
	}
}

// delegationDefaultMaxHops mirrors delegation.DefaultMaxHops. Duplicated
// rather than imported because config must not depend on the delegation
// package; a test in delegation asserts the two stay equal.
const delegationDefaultMaxHops = 10

// The investigation brakes, mirrored from delegation for the same reason
// and pinned by the same test.
const (
	delegationDefaultMaxIterations    = 5
	delegationDefaultMaxRuntimeMin    = 20
	delegationDefaultNoEvidenceRounds = 2
)
