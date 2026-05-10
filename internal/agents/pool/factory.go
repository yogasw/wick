package pool

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/gate"
	"github.com/yogasw/wick/internal/agents/provider"
	"github.com/yogasw/wick/internal/agents/provider/claude"
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
	// BypassPermissionsLoader (optional) is called on every Build to
	// check whether --permission-mode bypassPermissions should be added
	// when no gate is active. Useful for non-interactive channels
	// (Slack, HTTP) where the operator wants to skip prompts without
	// enabling the full command gate.
	BypassPermissionsLoader func() bool

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

	bypassPerms := false
	if f.BypassPermissionsLoader != nil {
		bypassPerms = f.BypassPermissionsLoader()
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
		spawner = claude.Spawner{Binary: bin, BypassPermissions: bypassPerms}
	}

	// Resolve active gate config: dynamic loader takes precedence so
	// UI changes take effect on the next spawn without server restart.
	activeGate := f.Gate
	if f.GateLoader != nil {
		activeGate = f.GateLoader()
	}

	if activeGate != nil {
		s, err := f.attachGateConfig(opt, spawner, activeGate)
		if err != nil {
			return BuildResult{}, fmt.Errorf("attach gate: %w", err)
		}
		spawner = s
		// Do NOT set BypassPermissions when the gate is active. claude
		// 2.1.138+ skips PreToolUse hooks under bypassPermissions mode,
		// which would silently disable the gate. The hook itself is
		// what suppresses the permission prompt — when it emits a deny
		// envelope, claude cancels the tool without asking the user.
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
	pType := opt.ProviderType
	if pType == "" {
		pType = string(provider.TypeClaude)
	}
	pName := opt.ProviderName
	if pName == "" {
		pName = pType
	}
	spawnStart := time.Now().UTC()
	if f.SpawnLogger != nil {
		spawnLogPath = f.SpawnLogger.Path(pType, pName, opt.SessionID, spawnStart)
		// Pre-start record: what we know before subprocess actually
		// runs. PID + first message land in a follow-up `start`
		// event written from OnStarted (after the pool drains the
		// buffer and reads the OS pid).
		_ = f.SpawnLogger.Append(spawnLogPath, provider.SpawnEvent{
			Type:         "start",
			At:           spawnStart,
			ProviderType: pType,
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
			ProviderType:     pType,
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
				ProviderType: pType,
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
