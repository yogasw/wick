package pool

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/gate"
	"github.com/yogasw/wick/internal/agents/preset"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/claude"
	codexpkg "github.com/yogasw/wick/internal/agents/provider/codex"
	geminipkg "github.com/yogasw/wick/internal/agents/provider/gemini"
	wickpkg "github.com/yogasw/wick/internal/agents/provider/wick"
	"github.com/yogasw/wick/internal/agents/state"
	"github.com/yogasw/wick/internal/agents/store"
	systemprompt "github.com/yogasw/wick/internal/agents/system-prompt"
	"github.com/yogasw/wick/pkg/safeexec"
)

// ClaudeFactory is the production AgentFactory: wires a ClaudeParser +
// ClaudeSpawner into a fresh provider.Agent for each Build call.
//
// The factory owns no per-spawn state; the pool calls Build once per
// session activation.
type ClaudeFactory struct {
	Layout    config.Layout
	Spawner   provider.Spawner // optional override; nil = real claude
	RecordRaw bool
	OnEvent   func(sessionID, agentName string, ev event.AgentEvent)
	OnExit    func(sessionID, agentName string, reason provider.ExitReason)

	// Gate (optional) attaches a static command whitelist to every spawn.
	// When non-nil, Build writes a per-session settings.json + spec
	// file to a temp dir, points the spawner at the settings file,
	// and injects WICK_GATE_SPEC into ExtraEnv so wick-gate finds
	// its config. nil = no gate (fail-open, only safe for tests).
	Gate *GateConfig
	// GateLoader (optional) is called on every Build to fetch the
	// current gate config from the live config store. Takes precedence
	// over Gate when non-nil. This lets operators toggle gate_enabled
	// or edit AllowedCmds in the UI without restarting the server.
	GateLoader func() *GateConfig
	// ToolMemoryLoader (optional) returns the ceiling for a shell command
	// the wick provider runs itself, in MB. 0 = unbounded. Read per Build
	// for the same reason as MemGuardLoader.
	ToolMemoryLoader func() int

	// MemGuardLoader (optional) returns the current memory-guard policy,
	// read on every Build so an operator can change the mode or the
	// limits in the UI without restarting the server — the same reason
	// GateLoader exists. nil, or a nil return, means the guard is off and
	// spawns behave exactly as they did before it existed.
	//
	// The per-instance ceiling is resolved here rather than by the caller,
	// since only Build knows which instance a session ended up on.
	MemGuardLoader func() *provider.MemGuard

	// PermissionModeLoader (optional) is called on every Build to read
	// the current GateConfig.PermissionMode value. Return "bypass" to
	// force --permission-mode bypassPermissions on Claude (and the
	// equivalent on codex/gemini) when no gate hook is installed.
	// Any other value (including empty) means "prompt as normal".
	PermissionModeLoader func() string

	// SystemPromptLoader (optional) returns a global system prompt
	// fragment appended to the loaded preset body on every spawn.
	// Empty string = no append. Lets operators set org-wide rules
	// (prompt-injection defenses, shared conventions) without editing
	// every preset. Preset stays the primary; this only adds to it.
	SystemPromptLoader func() string

	// SenderVisibilityLoader (optional) returns how much of a message
	// sender's identity reaches the model. Forwarded to spawners that replay
	// conversation.jsonl themselves, so a replayed turn is attributed the
	// same way the live send was. nil = store.SenderName.
	SenderVisibilityLoader func() string

	// TraceEventMaxKBLoader (optional) returns the current trace_event_max_kb
	// config value. 0 = no cap.
	TraceEventMaxKBLoader func() int

	// TraceInlineKBLoader (optional) returns the current trace_event_inline_kb
	// config value. Called on every Build so operators can change the threshold
	// without restarting the server. 0 or negative = use DefaultTraceInlineBytes.
	TraceInlineKBLoader func() int

	// ConnectorCatalogLoader (optional) returns a "## Available wick
	// connectors" markdown block listing the connectors the spawning
	// agent should prefer over hand-rolled HTTP. Wired in server.go
	// so the loader can call connectorsSvc and filter to instances
	// whose status is "ready" — connectors the operator has finished
	// configuring. Empty string = no append (no connectors ready, or
	// service unavailable). Inserted between the immutable rules and
	// the preset body so the catalog can't override either layer.
	ConnectorCatalogLoader func() string

	// TicketPointerLoader (optional) returns a short block naming the
	// ticket this session belongs to and how many notes it carries — a
	// COUNT, never the note bodies. It exists so the agent knows the
	// notes are there and reads them through the notes connector when
	// they matter, instead of the prompt growing with every note ever
	// written on a long-lived ticket. Empty string = nothing to point at
	// (no ticket, no notes) and nothing is appended.
	TicketPointerLoader func(sessionID string) string

	// SpawnLogger (optional) writes one jsonl per spawn under
	// `<base>/backends/spawns/`. Each spawn emits `start` on Build +
	// `exit` from the OnExit hook so the Backends UI can list spawn
	// history per backend by `ls`-ing the directory. nil = no logging.
	SpawnLogger *provider.SpawnLogger

	// MCPToken is the per-boot internal MCP secret forwarded to the
	// claude spawner so agents reach the live MCP server over loopback.
	// It maps to a synthetic ADMIN principal, so it is only a FALLBACK:
	// used when SessionMCPToken is unset, or when it declines to mint
	// (a session with no owner — legacy rows, cron, system jobs).
	MCPToken string

	// SessionMCPToken mints a per-SESSION MCP credential that authenticates
	// as the human who owns the session, instead of the shared admin
	// principal MCPToken grants. Without it, every user's spawn reaches the
	// MCP server as the same synthetic admin, so connector access control
	// and tag filtering never apply to a web chat.
	//
	// Returning ok=false means "no real owner" and the caller falls back to
	// MCPToken — an ownerless session must keep working rather than lose
	// MCP entirely. nil = per-user identity disabled (fallback for all).
	SessionMCPToken func(sessionID string) (token string, ok bool)

	// InstanceOverride pins a specific Instance for every Build call,
	// bypassing the provider.Find registry lookup. Tests use this to
	// inject ExtraArgs / Env without touching userconfig files.
	InstanceOverride *provider.Instance
}

// GateConfig describes the gate plumbing: where the wick-gate binary
// lives + what rules it enforces. The factory writes the shared
// spec.json from Rules on every spawn so UI changes propagate
// immediately without restarting the server.
type GateConfig struct {
	// GateBinary is the absolute path to the wick-gate binary. Required.
	GateBinary string
	// Rules is the whitelist enforced for every spawn under this factory.
	Rules []gate.CommandRule
	// AppName drives the shared spec path (~/.<app>/agents/gate/spec.json).
	// Falls back to "wick" when empty.
	AppName string
	// DefaultScope is written into spec.json as the fallback scope for
	// rules that have an empty Scope field. Typically the default
	// workspace directory so no-scope rules are still path-restricted.
	DefaultScope string
	// TempDirRoot is where per-spawn gate artifacts live. If empty,
	// `<Layout.SessionDir(id)>/gate` is used.
	TempDirRoot string
}

// Build returns a fresh agent + state machine + store wired for one
// session+agent. Caller (the pool) is responsible for calling
// agent.Start.
func (f *ClaudeFactory) Build(opt FactoryOptions) (BuildResult, error) {
	st := state.New(nil)
	st.SetIdentity(opt.SessionID, opt.AgentName)
	// Compute provider "type/name" for store stamping. Mirrors the
	// AgentEntry.Provider format used in agents.json so the UI can
	// render either source the same way.
	storeProviderType := opt.ProviderType
	if storeProviderType == "" {
		storeProviderType = string(provider.TypeClaude)
	}
	storeProviderName := opt.ProviderName
	if storeProviderName == "" {
		storeProviderName = storeProviderType
	}
	traceInlineBytes := 0
	if f.TraceInlineKBLoader != nil {
		if kb := f.TraceInlineKBLoader(); kb > 0 {
			traceInlineBytes = kb * 1024
		}
	}
	traceEventMaxBytes := 0
	if f.TraceEventMaxKBLoader != nil {
		if kb := f.TraceEventMaxKBLoader(); kb > 0 {
			traceEventMaxBytes = kb * 1024
		}
	}
	sto := store.New(store.Options{
		Layout:             f.Layout,
		SessionID:          opt.SessionID,
		AgentName:          opt.AgentName,
		Provider:           storeProviderType + "/" + storeProviderName,
		RecordRaw:          f.RecordRaw,
		TraceInlineBytes:   traceInlineBytes,
		TraceEventMaxBytes: traceEventMaxBytes,
	})

	// Layered system prompt (top wins on conflict):
	//   1. immutable wick rules (e.g. ban AskUserQuestion) — set in code
	//   2. preset body (per-preset persona)
	//   3. operator-edited `system_prompt` config row
	// Layer 1 must lead so its guards override anything the preset /
	// config below tries to relax.
	// Normalize provider type early so immutable prompt selection is correct.
	pTypeStrEarly := opt.ProviderType
	if pTypeStrEarly == "" {
		pTypeStrEarly = string(provider.TypeClaude)
	}
	// Audience — a sub-agent spawn gets the delegated-child overlay
	// (report_result, no ask_user) instead of the human-facing one
	// (render formats, session title, scheduling). Decided by the
	// session's parentage, which the pool read off meta; a role or
	// preset cannot override it.
	immutable := systemprompt.ImmutableFor(pTypeStrEarly, opt.IsSubAgent)
	presetContent := immutable
	if f.ConnectorCatalogLoader != nil {
		if catalog := strings.TrimSpace(f.ConnectorCatalogLoader()); catalog != "" {
			presetContent += "\n\n" + catalog
		}
	}
	if opt.PresetName != "" {
		if p, err := preset.Load(f.Layout, opt.PresetName); err == nil && strings.TrimSpace(p.Body) != "" {
			presetContent += "\n\n" + p.Body
		}
	}
	// Per-session free-text addon, after the named preset so it can
	// refine it. This is how a sub-agent role's system prompt reaches its
	// spawn — a role is not a named preset and must not pollute the
	// shared preset list.
	if addon := strings.TrimSpace(opt.SystemAddon); addon != "" {
		presetContent += "\n\n" + addon
	}
	if f.SystemPromptLoader != nil {
		if extra := strings.TrimSpace(f.SystemPromptLoader()); extra != "" {
			presetContent += "\n\n" + extra
		}
	}
	// Per-session identity block, appended last so it is the "This
	// session" block at the very end of the assembled prompt (the
	// immutable rules reference it by that name). The agent needs the
	// session_id for wick_session_info / wick_set_title / ask_user, and
	// having it in the system prompt means it is always available — not
	// only on the first turn where channels inject a one-time context
	// message.
	presetContent += "\n\n" + sessionIdentityBlock(opt.SessionID, opt.Origin, opt.Title, opt.TitleCustom)

	// Ticket / notes pointer — a COUNT and an id, never the note bodies.
	// A ticket accumulates notes for as long as the work lasts, so
	// inlining them would charge that growing cost on every turn forever;
	// the agent reads what it needs through the notes connector instead.
	// Fixed size, and omitted entirely when there is nothing to point at.
	if f.TicketPointerLoader != nil {
		if line := strings.TrimSpace(f.TicketPointerLoader(opt.SessionID)); line != "" {
			presetContent += "\n\n" + line
		}
	}

	bypassPerms := false
	if f.PermissionModeLoader != nil {
		bypassPerms = f.PermissionModeLoader() == "bypass"
	}

	// Normalize provider keys once — used by spawner dispatch, instance
	// lookup, and spawn-log naming.
	pTypeStr := pTypeStrEarly
	pType := provider.Type(pTypeStr)
	pName := opt.ProviderName
	if pName == "" {
		pName = pTypeStr
	}

	// Per-instance config: spawner reads Instance.Hooks every spawn so
	// UI toggles take effect on the next message without server restart.
	resolvedIns, _ := provider.Find(pType, pName)
	if f.InstanceOverride != nil {
		resolvedIns = *f.InstanceOverride
	}

	// GateBinary path resolved once per Build. Still consulted by the
	// legacy whitelist refresh below and threaded into every spawn so
	// the spawner can write workspace hook configs without coupling to
	// factory internals.
	gateBin := ""
	activeGate := f.Gate
	if f.GateLoader != nil {
		activeGate = f.GateLoader()
	}
	if activeGate != nil {
		gateBin = activeGate.GateBinary
	}

	// claudeMCPToken is the per-session MCP credential handed to a claude
	// spawn, reported via BuildResult so the pool can revoke it on exit.
	// Empty when the spawn used the shared per-boot token (not revocable).
	var claudeMCPToken string
	spawner := f.Spawner
	if spawner == nil {
		bin, src := resolveProviderBinary(opt.ProviderType, opt.ProviderName)
		log.Info().
			Str("session", opt.SessionID).
			Str("provider_type", opt.ProviderType).
			Str("provider_name", opt.ProviderName).
			Str("binary", bin).
			Str("source", src).
			Msg("agents.spawn: resolve provider")
		switch pType {
		case provider.TypeCodex:
			// Same per-session credential claude gets, so a codex spawn also
			// reaches wick's tools as the human behind the session rather than
			// having no wick surface at all.
			tok := f.mcpTokenFor(opt.SessionID)
			if tok != f.MCPToken {
				// Per-session credential: revocable when the process dies. The
				// shared per-boot token is not, so it stays unreported.
				claudeMCPToken = tok
			}
			spawner = codexpkg.Spawner{Binary: bin, MCPToken: tok}
		case provider.TypeGemini:
			spawner = geminipkg.Spawner{Binary: bin, YoloMode: bypassPerms}
		case provider.TypeWick:
			// In-process runtime — no binary. Must NOT fall through to
			// the claude default: that would spawn a real claude CLI
			// under the wick label.
			spawner = wickpkg.Spawner{}
		default:
			// Mint ONCE: calling mcpTokenFor twice would issue two tokens
			// and leak the one not handed to the spawner.
			tok := f.mcpTokenFor(opt.SessionID)
			if tok != f.MCPToken {
				// Per-session credential: revocable when the process dies.
				// The shared per-boot token is not, so it stays unreported.
				claudeMCPToken = tok
			}
			spawner = claude.Spawner{Binary: bin, BypassPermissions: bypassPerms, MCPToken: tok}
		}
	}

	// Legacy gate spec refresh: still rewrites the shared spec.json so
	// AllowedCmds is current at next gate-binary invocation. attachGate
	// no longer mutates the spawner — hook config now lives inside the
	// spawner's applyHookConfig and is driven by Instance.Hooks intent.
	if activeGate != nil {
		if _, err := f.attachGateConfig(opt, spawner, activeGate); err != nil {
			log.Warn().Err(err).Msg("agents.spawn: gate spec refresh failed")
		}
	}

	var onEvent func(event.AgentEvent)
	if f.OnEvent != nil {
		sid, name := opt.SessionID, opt.AgentName
		onEvent = func(ev event.AgentEvent) { f.OnEvent(sid, name, ev) }
	}

	// Spawn-log: one file per spawn, named so `ls` filters by
	// {type, name, session} without opening files. The path is
	// captured here at Build time so both the synchronous start
	// event and the async exit hook write to the same file.
	var spawnLogPath string
	spawnStart := time.Now().UTC()
	if f.SpawnLogger != nil {
		spawnLogPath = f.SpawnLogger.Path(pTypeStr, pName, opt.SessionID, spawnStart)
		// Pre-start record: what we know before subprocess actually
		// runs. PID + first message land in a follow-up `start`
		// event written from OnStarted (after the pool drains the
		// buffer and reads the OS pid).
		_ = f.SpawnLogger.Append(spawnLogPath, provider.SpawnEvent{
			Type:         "start",
			At:           spawnStart,
			ProviderType: pTypeStr,
			ProviderName: pName,
			SessionID:    opt.SessionID,
			AgentName:    opt.AgentName,
			Workspace:    opt.Workspace,
			ResumeID:     opt.ResumeID,
			Origin:       opt.Origin,
		})
	}

	writeStartEvent := func(pid int, binary string, argv, env []string, firstMsg string) {
		if f.SpawnLogger == nil || spawnLogPath == "" {
			return
		}
		_ = f.SpawnLogger.Append(spawnLogPath, provider.SpawnEvent{
			Type:             "start",
			At:               time.Now().UTC(),
			ProviderType:     pTypeStr,
			ProviderName:     pName,
			SessionID:        opt.SessionID,
			AgentName:        opt.AgentName,
			PID:              pid,
			Binary:           binary,
			Args:             argv,
			Env:              env,
			FirstUserMessage: provider.TruncateFirstMessage(firstMsg),
		})
	}

	onStarted := func(meta SpawnStartMeta) {
		// Respawn-per-turn providers (codex) have no live process at the
		// pool's post-Start hook, so PID/Argv are empty here — the real
		// start event comes via OnSpawn below. Skip the blank record so
		// the spawn log doesn't show a start with no Reproduce command.
		if meta.PID == 0 && len(meta.Argv) == 0 {
			return
		}
		writeStartEvent(meta.PID, meta.Binary, meta.Argv, meta.Env, meta.FirstUserMessage)
	}

	// onExitDetail writes the exit event to the spawn log with the full
	// reason context (why it was killed / crashed, exit code, stderr
	// tail). It fires alongside OnExit; the pool slot-release still runs
	// off the enum via f.OnExit below so its contract is unchanged.
	onExitDetail := func(d provider.ExitDetail) {
		if f.SpawnLogger != nil && spawnLogPath != "" {
			_ = f.SpawnLogger.Append(spawnLogPath, provider.SpawnEvent{
				Type:         "exit",
				At:           time.Now().UTC(),
				ProviderType: pTypeStr,
				ProviderName: pName,
				SessionID:    opt.SessionID,
				AgentName:    opt.AgentName,
				ExitReason:   exitReasonString(d.Reason),
				ReasonDetail: d.ReasonDetail,
				ExitCode:     d.ExitCode,
				StderrTail:   d.StderrTail,
				Error:        d.WaitErr,
				DurationMs:   time.Since(spawnStart).Milliseconds(),
			})
		}
	}
	onExit := func(r provider.ExitReason) {
		if f.OnExit != nil {
			f.OnExit(opt.SessionID, opt.AgentName, r)
		}
	}

	insCopy := resolvedIns

	// Memory guard, resolved per instance: the global ceiling is the
	// default, and an instance may set its own — higher OR lower. See
	// config.ResolveAgentLimitMB for why this is not a min().
	var memGuard *provider.MemGuard
	if f.MemGuardLoader != nil {
		if g := f.MemGuardLoader(); g != nil {
			resolved := *g
			resolved.AgentLimitMB = config.ResolveAgentLimitMB(resolvedIns.MemoryMaxMB, g.AgentLimitMB)
			memGuard = &resolved
		}
	}

	a := provider.New(provider.Options{
		Workspace:     opt.Workspace,
		SessionDir:    f.Layout.SessionDir(opt.SessionID),
		SessionID:     opt.SessionID,
		ResumeID:      opt.ResumeID,
		IdleTimeout:   opt.IdleTimeout,
		KillAfterIdle: opt.KillAfterIdle,
		ParserFactory: func() event.Parser {
			if pType == provider.TypeCodex {
				return event.NewCodexParser()
			}
			return event.NewClaudeParser()
		},
		Spawner:      spawner,
		Store:        sto,
		State:        st,
		OnEvent:      onEvent,
		OnExit:       onExit,
		OnExitDetail: onExitDetail,
		OnSpawn: func(binary string, argv []string, env []string, pid int, firstMsg string) {
			writeStartEvent(pid, binary, argv, env, firstMsg)
		},
		Instance:   &insCopy,
		GateBinary: gateBin,
		Preset:     presetContent,
		// Only the wick provider reads this (it rebuilds prompts from
		// conversation.jsonl); the CLI providers resume from their own
		// transcript and ignore it.
		SenderVisibility: func() string {
			if f.SenderVisibilityLoader == nil {
				return store.SenderName
			}
			return store.NormalizeSenderVisibility(f.SenderVisibilityLoader())
		}(),
		MaxTurns:       opt.MaxTurns,
		ThinkingTokens: opt.ThinkingTokens,
		ModelID:        opt.ModelID,
		MemGuard:       memGuard,
		// Only applied when the guard is on: a tool ceiling without a mode
		// would limit commands the operator never asked to limit.
		ToolMemoryMaxMB: func() int {
			if memGuard == nil || f.ToolMemoryLoader == nil {
				return 0
			}
			return f.ToolMemoryLoader()
		}(),
		ExtraArgs: resolvedIns.ExtraArgs,
		ExtraEnv:  resolvedIns.Env,
		// claude = persistent stdin (append); codex = one-shot per turn,
		// queue mid-turn sends so spam doesn't stack subprocesses. A
		// per-instance override (providers UI) takes precedence over the
		// type default.
		SendMode: sendModeFor(pType, resolvedIns.SendMode),
	})
	return BuildResult{Agent: a, State: st, Store: sto, OnStarted: onStarted, MCPToken: claudeMCPToken}, nil
}

// sendModeFor resolves an instance's Send behaviour. A non-empty
// per-instance override (set in the providers UI: "append" | "queue" |
// "spawn") wins; otherwise it falls back to the provider type's default:
// codex is one-shot per turn (respawn + queue mid-turn sends); claude /
// gemini keep a persistent stdin and append.
// sessionIdentityBlock renders the per-session identity appended to the
// system prompt so the agent always knows its session_id (needed by
// wick_session_info / wick_set_title / ask_user), which channel it is on,
// and the current title state. title_custom lets the agent decide
// whether to set a title without a wick_session_info round-trip — when
// false it should derive one and call wick_set_title; when true the
// title was already chosen and must be left alone. The values are a
// snapshot at spawn time. channel falls back to "ui" when origin is
// unset.
func sessionIdentityBlock(sessionID, channel, title string, titleCustom bool) string {
	if strings.TrimSpace(channel) == "" {
		channel = "ui"
	}
	var b strings.Builder
	b.WriteString("## This session\n\n")
	b.WriteString("These identify the conversation you are in. Pass session_id")
	b.WriteString(" to any wick tool that needs it (wick_session_info,")
	b.WriteString(" wick_set_title, ask_user) instead of guessing.\n\n")
	b.WriteString("session_id: ")
	b.WriteString(sessionID)
	b.WriteString("\nchannel: ")
	b.WriteString(channel)
	b.WriteString("\ntitle: ")
	b.WriteString(title)
	b.WriteString("\ntitle_custom: ")
	if titleCustom {
		b.WriteString("true")
	} else {
		b.WriteString("false")
	}
	return b.String()
}

// mcpTokenFor picks the MCP credential for one spawn: a per-session token
// naming the owning human when one can be minted, else the shared internal
// token (synthetic admin).
//
// The fallback is deliberate, not lazy. Sessions predating ownership
// tracking carry no UserID, and cron / system spawns have no human at all;
// refusing to spawn them, or spawning them with no MCP access, would break
// working setups to enforce an attribution nobody asked for there.
func (f *ClaudeFactory) mcpTokenFor(sessionID string) string {
	if f.SessionMCPToken != nil && sessionID != "" {
		if tok, ok := f.SessionMCPToken(sessionID); ok && tok != "" {
			return tok
		}
	}
	return f.MCPToken
}

func sendModeFor(pType provider.Type, override string) provider.SendMode {
	if m, ok := provider.ParseSendMode(override); ok {
		return m
	}
	if pType == provider.TypeCodex {
		return provider.SendRespawnQueue
	}
	return provider.SendAppend
}

// attachGateConfig writes the gate hook into the workspace's
// .claude/settings.local.json so Claude's project-scoped hook loader
// picks it up, and returns the (unmodified) spawner.
//
// Claude does NOT honour hooks injected via --settings; they must live
// in the standard settings hierarchy. We write to the workspace's
// .local variant to avoid stomping committed settings.json files.
//
// Rules + AutoApproved live in the shared spec at
// gate.SharedSpecPath(AppName); rewritten on every spawn so UI
// changes propagate without a server restart.
func (f *ClaudeFactory) attachGateConfig(opt FactoryOptions, base provider.Spawner, cfg *GateConfig) (provider.Spawner, error) {
	// Refresh the shared spec so the gate binary picks up the latest
	// rules on this spawn. AppName empty falls back to "wick".
	appName := cfg.AppName
	if appName == "" {
		appName = "wick"
	}
	_ = gate.WriteSharedSpec(appName, gate.Spec{Rules: cfg.Rules, DefaultScope: cfg.DefaultScope})

	// Write hook into the workspace so Claude discovers it via the
	// standard project-scoped settings hierarchy.
	workspace := opt.Workspace
	if workspace == "" {
		workspace = cfg.TempDirRoot
	}
	if workspace == "" && opt.SessionID != "" {
		workspace = filepath.Join(f.Layout.SessionDir(opt.SessionID), "gate")
	}
	if workspace != "" {
		if err := gate.WriteWorkspaceHooks(workspace, cfg.GateBinary); err != nil {
			return base, fmt.Errorf("write workspace hooks: %w", err)
		}
	}

	return base, nil
}

// resolveProviderBinary picks the binary the spawner should exec for
// a given provider {type, name}: the per-instance Binary override
// (set via /tools/agents/providers UI) wins, else PATH lookup of the
// type name, else empty (Spawner falls back to bare type name).
//
// Returned source is one of: registry, path, unconfigured — surfaced
// in the spawn log so a "claude not found" failure tells the operator
// whether the registry path was wrong vs whether they never set one.
func resolveProviderBinary(providerType, providerName string) (bin, source string) {
	t := provider.Type(providerType)
	if t == "" {
		t = provider.TypeClaude
	}
	if ins, err := provider.Find(t, providerName); err == nil && ins.Binary != "" {
		return ins.Binary, "registry"
	}
	if p, err := safeexec.LookPath(string(t)); err == nil {
		return p, "path"
	}
	// provider.ResolveBin covers PATH plus the per-OS install locations, which
	// is what finds a CLI installed via npm/curl outside PATH. Kept as its own
	// step so the "scan" source label stays meaningful in the spawn log.
	if p, err := provider.ResolveBin(provider.Instance{Type: t, Name: providerName}); err == nil {
		return p, "scan"
	}
	return "", "unconfigured"
}

// exitReasonString maps the typed ExitReason to the short label
// used in spawn-log files. The label is what the Backends UI
// renders, so keep it stable across the codebase.
func exitReasonString(r provider.ExitReason) string {
	switch r {
	case provider.ExitClean:
		return "clean"
	case provider.ExitIdle:
		return "idle"
	case provider.ExitStopped:
		return "stopped"
	case provider.ExitError:
		return "error"
	case provider.ExitRespawn:
		return "respawn"
	case provider.ExitOOM:
		return "oom"
	}
	return "unknown"
}
