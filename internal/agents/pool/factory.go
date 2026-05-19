package pool

import (
	"context"
	"fmt"
	"os/exec"
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
	"github.com/yogasw/wick/internal/agents/state"
	"github.com/yogasw/wick/internal/agents/store"
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

	// SpawnLogger (optional) writes one jsonl per spawn under
	// `<base>/backends/spawns/`. Each spawn emits `start` on Build +
	// `exit` from the OnExit hook so the Backends UI can list spawn
	// history per backend by `ls`-ing the directory. nil = no logging.
	SpawnLogger *provider.SpawnLogger
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
	sto := store.New(store.Options{
		Layout:    f.Layout,
		SessionID: opt.SessionID,
		AgentName: opt.AgentName,
		RecordRaw: f.RecordRaw,
	})

	// Layered system prompt (top wins on conflict):
	//   1. immutable wick rules (e.g. ban AskUserQuestion) — set in code
	//   2. preset body (per-preset persona)
	//   3. operator-edited `system_prompt` config row
	// Layer 1 must lead so its guards override anything the preset /
	// config below tries to relax.
	presetContent := config.ImmutableSystemPrompt()
	if opt.PresetName != "" {
		if p, err := preset.Load(f.Layout, opt.PresetName); err == nil && strings.TrimSpace(p.Body) != "" {
			presetContent += "\n\n" + p.Body
		}
	}
	if f.SystemPromptLoader != nil {
		if extra := strings.TrimSpace(f.SystemPromptLoader()); extra != "" {
			presetContent += "\n\n" + extra
		}
	}

	bypassPerms := false
	if f.PermissionModeLoader != nil {
		bypassPerms = f.PermissionModeLoader() == "bypass"
	}

	// Normalize provider keys once — used by spawner dispatch, instance
	// lookup, and spawn-log naming.
	pTypeStr := opt.ProviderType
	if pTypeStr == "" {
		pTypeStr = string(provider.TypeClaude)
	}
	pType := provider.Type(pTypeStr)
	pName := opt.ProviderName
	if pName == "" {
		pName = pTypeStr
	}

	// Per-instance config: spawner reads Instance.Hooks every spawn so
	// UI toggles take effect on the next message without server restart.
	resolvedIns, _ := provider.Find(pType, pName)

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
			spawner = codexpkg.Spawner{Binary: bin}
		case provider.TypeGemini:
			spawner = geminipkg.Spawner{Binary: bin, YoloMode: bypassPerms}
		default:
			spawner = claude.Spawner{Binary: bin, BypassPermissions: bypassPerms}
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
		})
	}

	onStarted := func(meta SpawnStartMeta) {
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
			PID:              meta.PID,
			Binary:           meta.Binary,
			Args:             meta.Argv,
			FirstUserMessage: provider.TruncateFirstMessage(meta.FirstUserMessage),
		})
	}

	onExit := func(r provider.ExitReason) {
		if f.SpawnLogger != nil && spawnLogPath != "" {
			_ = f.SpawnLogger.Append(spawnLogPath, provider.SpawnEvent{
				Type:         "exit",
				At:           time.Now().UTC(),
				ProviderType: pTypeStr,
				ProviderName: pName,
				SessionID:    opt.SessionID,
				AgentName:    opt.AgentName,
				ExitReason:   exitReasonString(r),
				DurationMs:   time.Since(spawnStart).Milliseconds(),
			})
		}
		if f.OnExit != nil {
			f.OnExit(opt.SessionID, opt.AgentName, r)
		}
	}

	insCopy := resolvedIns
	a := provider.New(provider.Options{
		Workspace:     opt.Workspace,
		ResumeID:      opt.ResumeID,
		IdleTimeout:   opt.IdleTimeout,
		KillAfterIdle: opt.KillAfterIdle,
		ParserFactory: func() event.Parser { return event.NewClaudeParser() },
		Spawner:       spawner,
		Store:         sto,
		State:         st,
		OnEvent:       onEvent,
		OnExit:        onExit,
		Instance:      &insCopy,
		GateBinary:    gateBin,
		Preset:        presetContent,
	})
	return BuildResult{Agent: a, State: st, Store: sto, OnStarted: onStarted}, nil
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
	if p, err := exec.LookPath(string(t)); err == nil {
		return p, "path"
	}
	if st := provider.Probe(context.Background(), provider.Instance{Type: t, Name: providerName}); st.PathFound {
		return st.Path, "scan"
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
	}
	return "unknown"
}
