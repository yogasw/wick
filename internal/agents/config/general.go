package config

import systemprompt "github.com/yogasw/wick/internal/agents/system-prompt"

// GeneralConfig holds top-level Agents knobs. Reflected into the
// configs table via pkg/entity.StructToConfigs (Owner = "agents" at
// registration time). See agents-design.md §8.1.
// Gate settings live in GateConfig; channel settings in SlackChannelConfig.
type GeneralConfig struct {
	Enabled          bool   `wick:"bool;group=General|Top-level Agents switches and defaults.;desc=Enable the Agents feature."`
	DefaultProvider  string `wick:"dropdown;key=default_provider;group=General;desc=Default provider instance for new sessions when none is picked (channels, API, quick-create). Options are your configured provider instances (type or type/name); empty falls back to claude."`
	PublicURL        string `wick:"url;group=General;desc=Public base URL of this wick instance. Used for the dashboard meta-command."`
	MaxConcurrent    int    `wick:"number;group=Concurrency & Lifecycle|How many agent subprocesses run at once and when idle ones are reclaimed.;desc=Max concurrent agent subprocesses across all providers. 0 = unlimited. Default: 2."`
	IdleTimeoutSec   int    `wick:"number;group=Concurrency & Lifecycle;desc=Seconds of inactivity before subprocess is killed. Default: 120."`
	KillAfterIdleSec int    `wick:"number;group=Concurrency & Lifecycle;desc=Extra seconds after idle timeout before the subprocess is killed. 0 = kill immediately at idle timeout. Default: 0."`
	PreemptIdle      bool   `wick:"bool;group=Concurrency & Lifecycle;desc=When the pool is full and a new session is queued, preempt the longest-idle active subprocess to free its slot. Killed sessions resume via --resume on their next message."`
	AutoRescan       bool   `wick:"bool;group=Concurrency & Lifecycle;desc=Auto re-probe provider binaries when cached version is older than 24h. Off = refresh only via Rescan button."`
	// Per-user identity for shared sessions. A running subprocess carries the
	// MCP credential of whoever spawned it (it sits in the process argv and
	// cannot be swapped in place), so a second user sending into the same
	// session would otherwise act with the first user's connector access.
	// Creating a wick account is an INSTALL-level decision, not a per-channel
	// one. Channel rows are per-owner: any user who can add their own Slack bot
	// would otherwise be able to turn auto-registration on for it, and thereby
	// mint pending wick accounts. Keeping the switch here means only an admin
	// editing the Agents settings can allow that.
	ChannelAutoRegister bool `wick:"bool;key=channel_auto_register;group=Session Identity;desc=When someone messages an agent from a chat channel and their email has no wick account, create one automatically. The account is created PENDING APPROVAL — an admin approves it under Admin → Users before that person can do anything, so this only saves the invite step, it never grants access. Off = an unmatched sender is told to ask an admin for an invite. Guests, bots, and senders with no readable email are always refused either way."`

	RespawnOnCallerChange bool `wick:"bool;key=respawn_on_caller_change;group=Session Identity|How a session behaves when more than one person talks to it.;desc=When another user sends a message into a session already running for someone else, restart the subprocess so the new turn runs under that user's own identity and connector access. Off = the running process is reused and the turn inherits the original user's access. Costs the process's in-memory context on each handover (conversation history is reloaded)."`

	// How much of a sender's identity is repeated to the model on every
	// message. This is a privacy decision, and it is deliberately separate
	// from what the dashboard shows: the dashboard always renders the full
	// sender (name, channel, avatar) because a person reading a shared thread
	// needs to tell the participants apart. What the model receives is a
	// different question — it is repeated into every turn, it is billed as
	// tokens, and on most installs the model has no use for a platform user
	// ID at all.
	SenderVisibility string `wick:"dropdown=off|name|name_id|full;key=sender_visibility;group=Session Identity;desc=How much about the sender is written into each message the agent receives. name (default) = just their display name, so the agent can address people correctly in a shared thread. off = nothing, and the agent cannot tell participants apart. name_id = adds the platform user ID, for agents that need to @-mention or DM a specific person. full = also adds the channel and handle. The dashboard always shows the full sender regardless of this setting, and none of the options let a message body override who the sender is."`

	// Memory guard. MaxConcurrent above counts PROCESSES; these count
	// BYTES. One slot is an idle agent at ~150 MB or an agent driving a
	// browser at ~2 GB, and the pool cannot tell them apart — which is how
	// a single runaway agent takes the whole server down with it.
	MemoryGuardMode string `wick:"dropdown=off|measure|enforce;group=Memory Guard|Keep one runaway agent from taking the whole machine down. Start at 'measure' to learn the real numbers on this machine, then switch to 'enforce'.;desc=off = no memory management at all (default). measure = put each agent in its own group and record exactly how much it used, without limiting anything — use this first to learn safe numbers. enforce = the same recording, PLUS the kernel stops an agent that goes over its limit, leaving every other agent and the server itself untouched. Enforce never records less than measure. Enforcing needs a Linux kernel — either a systemd user session or a writable cgroup filesystem, so a container without systemd still enforces. On a machine with neither (Windows, macOS) enforce automatically behaves like measure, and the Resources page says so rather than pretending agents are protected. Both modes only cover agents wick starts itself; a claude or codex you run by hand in a terminal is outside wick and needs the 'wrapper' method below."`
	// MemoryGuardMethod is the pre-2026-08 single choice, kept only so an
	// existing config keeps loading. Migrated to the two switches below on
	// read — see ResolveGuardScopes.
	//
	// Hidden rather than deleted: the row has to keep existing for the
	// migration to read, but offering it beside the switches that
	// replaced it would give two controls for one decision.
	MemoryGuardMethod   string `wick:"dropdown=auto|wrapper;hidden;group=Memory Guard;desc=Replaced by the two switches below. Kept so an existing setting still applies."`
	GuardOnSpawn        bool   `wick:"bool;group=Memory Guard;desc=Apply the limit to agents wick starts. This is the one that keeps working when nothing else does: it needs no files on disk and no root, so it survives a package update replacing the agent binary. Leave this on unless something outside wick is definitely covering the same agents."`
	GuardOnPath         bool   `wick:"bool;group=Memory Guard;desc=Also apply the limit to agents started outside wick — a claude or codex you run by hand in a terminal, or another service on this machine. Works by putting a small script in front of the binary, installed from the Resources page (it needs one root command, which is printed for you to run). Two things it cannot do: a caller that runs the binary by its full path never passes through it, and if a package update replaces the link the coverage silently stops — which is why leaving 'on spawn' on as well is worth it. Running both is safe: the kernel applies every ceiling and the tighter one wins."`
	AgentMemoryMaxMB    int    `wick:"number;group=Memory Guard;desc=Memory limit for one agent, in MB, counting everything it starts (browsers, tools, scripts). 0 = no limit. A provider instance can set its own value that overrides this one, higher or lower."`
	AgentsTotalMemoryMB int    `wick:"number;group=Memory Guard;desc=Combined memory limit across all running agents, in MB. A backstop for when several well-behaved agents add up to more than the machine has. 0 = no combined limit."`
	ToolMemoryMaxMB     int    `wick:"number;group=Memory Guard;desc=Memory limit in MB for a command an agent runs itself (grep, curl, scripts). Going over fails that one command and returns an error the agent can react to — the agent keeps running. 0 = no limit."`
	MinFreeMemoryMB     int    `wick:"number;group=Memory Guard;desc=Queue a new agent instead of starting it while free memory is below this many MB. Prevents the start that pushes the machine over the edge. 0 = start regardless."`
	ProtectWickFromOOM  bool   `wick:"bool;group=Memory Guard;desc=When memory runs out system-wide, tell the kernel to stop an agent rather than wick itself. Only applies in 'enforce' mode. Recommended: on."`

	// Contention controls, written onto the shared agents.slice in enforce
	// mode. Memory is the only control that kills; these shape how agents
	// COMPETE — with wick and with each other. All default to 0 = leave
	// the kernel default.
	AgentsCPUWeight   int `wick:"number;group=Memory Guard;desc=CPU priority of agents when the CPU is busy, relative to the rest of the system (default weight is 100). Set below 100 (e.g. 50) so wick and the OS stay responsive while agents work; agents still use all idle CPU. 0 = no preference. Only applies in 'enforce' mode."`
	AgentsCPUQuotaPct int `wick:"number;group=Memory Guard;desc=Hard cap on combined CPU of all agents, as a percentage of one core (100 = one full core, 200 = two). Slows heavy work down even when the machine is idle, so most setups should leave this at 0 = no cap and rely on the priority setting above. Only applies in 'enforce' mode."`
	AgentsTasksMax    int `wick:"number;group=Memory Guard;desc=Maximum number of processes and threads all agents may have at once. Stops a runaway script that keeps starting processes — thousands of tiny ones can freeze a machine while staying under every memory limit. 512 is a generous ceiling. 0 = no limit. Only applies in 'enforce' mode."`
	AgentsIOWeight    int `wick:"number;group=Memory Guard;desc=Disk-access priority of agents when the disk is busy, relative to the rest of the system (default weight is 100). Set below 100 so heavy agent file work does not starve wick. 0 = no preference. Only applies in 'enforce' mode."`

	// Usage history. Independent of the guard mode: measuring is how an
	// operator learns what to set, so it must work while the guard is off.
	ResourceHistoryEnabled    bool   `wick:"bool;group=Usage History|Record memory, CPU, and disk use per agent over time so the Resources page can show trends instead of a single instant. Works with the memory guard switched off — this is how you learn what to set.;desc=Record usage samples. Off = no sampling and the Resources page shows only a live snapshot."`
	ResourceSampleIntervalSec int    `wick:"number;group=Usage History;desc=Seconds between samples. Shorter shows brief spikes (a browser opening) but stores more points; 15 is a good balance. Default: 15."`
	ResourceRetentionMinutes  int    `wick:"number;group=Usage History;desc=How long to keep samples, in minutes. Anything older is discarded automatically. 360 = 6 hours (default), 1440 = one day. Lowering this frees memory immediately."`
	ResourceHistoryMaxPoints  int    `wick:"number;group=Usage History;desc=Hard ceiling on stored samples, whatever the retention window says. Protects against a very short interval filling memory. Default: 4096."`
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

	// HTML-artifact widget CSP. Artifacts are model-authored, so the
	// default posture is fully sealed. WidgetMode is the ONE knob that
	// decides the posture; every field after it is read only when the mode
	// is "custom". See widget.go. Projects may override the whole lot at
	// /manager/agents/projects.
	WidgetMode        string `wick:"dropdown=secure|unsecure|custom;group=Widget|How much HTML widgets are allowed to do. Widget HTML is written by the agent, so 'secure' is the default and the safe choice. This one setting decides everything — the fields below it apply only when you pick 'custom'.;desc=secure = sealed off: no embeds, no external images, media, scripts, or network calls, and links cannot open a tab (default). unsecure = everything allowed, including scripts loaded from any host — such a script can read whatever the widget holds and send it anywhere, so use it only for projects you trust. custom = choose each one yourself below."`
	WidgetFrameSrc    string `wick:"dropdown=block|list|all;group=Widget;desc=CUSTOM ONLY. Nested iframes inside a widget (Google Maps, YouTube embeds). block = no embeds at all."`
	WidgetImgSrc      string `wick:"dropdown=block|list|all;group=Widget;desc=CUSTOM ONLY. External images. Inline data: images always work regardless of this setting."`
	WidgetMediaSrc    string `wick:"dropdown=block|list|all;group=Widget;desc=CUSTOM ONLY. External audio and video. Inline data: media always works regardless of this setting."`
	WidgetConnectSrc  string `wick:"dropdown=block|list|all;group=Widget;desc=CUSTOM ONLY. fetch, XHR, and WebSocket from inside a widget. Anything permitted here can also send data out to that host."`
	WidgetScriptSrc   string `wick:"dropdown=block|list|all;group=Widget;desc=CUSTOM ONLY. Scripts loaded from another host. A permitted script runs inside the widget and can read everything the widget holds, so pair it with a narrow allowlist. The widget's own inline scripts always run either way."`
	WidgetAllowPopups bool   `wick:"bool;group=Widget;desc=CUSTOM ONLY. Let widget links open a new tab (target=_blank and window.open). A new tab is not covered by this policy, so it can reach any host regardless of the allowlist."`

	WidgetAllowPopupEscape bool `wick:"bool;group=Widget;desc=CUSTOM ONLY. Give the new tab a real origin instead of the sandboxed 'null' one it inherits by default. Without this, many sites load visibly broken in the new tab: they see Origin: null, so their own requests fail their CORS check. The trade-off is that the escaped tab runs outside this policy altogether. Implies the setting above."`

	WidgetAllowlist string `wick:"textarea;group=Widget;desc=CUSTOM ONLY. Hosts the 'list' settings above may reach — one per line, e.g. maps.google.com or *.example.com. https:// is assumed; plaintext http:// and paths are rejected. Projects append their own hosts to this list."`

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

	// Boot markers. SetOwned refuses a key with no declared meta row, so
	// a marker that is not listed here is silently never persisted — the
	// role seeder's marker had exactly that bug, which made every boot
	// re-run it (and would have re-flipped a role an operator had moved
	// back to foreground, forever). Hidden: the UI has no business
	// showing them, but the declaration is what makes the write legal.
	SubAgentsSeededRoles       string `wick:"hidden;key=sub_agents_seeded_roles_v1;desc=Boot marker: the investigation roles have been seeded once. Managed by wick — do not edit."`
	SubAgentsBackgroundDefault string `wick:"hidden;key=sub_agents_background_default_v1;desc=Boot marker: pre-existing roles were moved to the background default once. Managed by wick — do not edit."`
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
		AutoRescan:  true,
		PreemptIdle: true,
		// Ships OFF: recycling the subprocess costs its in-memory context,
		// which is the wrong trade for the common single-user session. Opt in
		// where a session is genuinely shared and attribution matters.
		RespawnOnCallerChange: false,
		// Off by default: an inbound message should not be able to create an
		// account until an operator decides that is wanted.
		ChannelAutoRegister: false,
		// The name alone: enough for the agent to address people correctly in
		// a shared thread, without repeating a platform user ID into every
		// turn on installs that have no use for one. The dashboard shows the
		// full sender either way.
		SenderVisibility: "name",
		SystemPrompt:        systemprompt.DefaultSystemPrompt(),
		WorkflowGuardMode:   "off",
		TraceEventInlineKB:  10,
		TraceEventMaxKB:     512,
		AirouterEnabled:     true,
		// Memory guard ships OFF: an install that never opts in must behave
		// byte-identically to one built before the feature existed. The four
		// numeric limits stay zero here on purpose — their correct values
		// depend on the machine, so they are derived from detected RAM at
		// first boot (DeriveMemoryDefaults) rather than guessed in a struct
		// literal that cannot see it.
		MemoryGuardMode: MemGuardOff,
		// Both scopes default off because the guard itself ships off.
		// Turning the mode on without a scope would enforce nothing, so
		// DeriveMemoryDefaults switches GuardOnSpawn on at first boot.
		MemoryGuardMethod:  MethodAuto,
		ProtectWickFromOOM: true,
		// Machine-independent safe values, unlike the byte limits above
		// which must be derived from RAM. Weight 50 = agents yield to wick
		// under load, full speed when idle; 512 tasks is generous for real
		// work while stopping a fork bomb. Quota and IO stay 0 (off):
		// a CPU cap slows legitimate work even on an idle machine.
		AgentsCPUWeight: 50,
		AgentsTasksMax:  512,
		// History defaults ON: it changes nothing about how agents run,
		// costs one /proc walk every 15s, and is the only way an operator
		// can pick limits from measurement instead of guesswork. Bounded
		// by both a 6h window and a hard point ceiling.
		ResourceHistoryEnabled:    true,
		ResourceSampleIntervalSec: 15,
		ResourceRetentionMinutes:  360,
		ResourceHistoryMaxPoints:  4096,
		// Widget CSP ships sealed — identical to the hardcoded policy that
		// preceded this config, so a fresh install and an upgraded one behave
		// the same. Seeded explicitly (rather than left empty) so the
		// dropdowns render with a selected value; an empty or unknown value
		// still resolves to secure/block, so a cleared row fails closed
		// either way. The per-directive seeds matter only if the operator
		// later switches the mode to custom.
		WidgetMode:             PresetSecure,
		WidgetFrameSrc:         ModeBlock,
		WidgetImgSrc:           ModeBlock,
		WidgetMediaSrc:         ModeBlock,
		WidgetConnectSrc:       ModeBlock,
		WidgetScriptSrc:        ModeBlock,
		WidgetAllowPopups:      false,
		WidgetAllowPopupEscape: false,
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
