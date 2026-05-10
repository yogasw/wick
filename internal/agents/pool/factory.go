package pool

import (
	"fmt"
	"path/filepath"
	"time"

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

	// Gate (optional) attaches the gate sidecar to every spawn. When
	// non-nil, Build writes a per-spawn settings.json pointing claude
	// at the gate binary; the gate binary then loads its rules + auto-
	// approved list from the shared spec at SharedSpecPath(AppName).
	// nil = no gate (fail-open, only safe for tests).
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

// GateConfig describes the gate plumbing: where the gate binary
// lives. Rules + auto-approved list now live in the shared spec
// (see gate.WriteSharedSpec) — the daemon writes them at boot and
// rewrites on revoke / always-allow, and the gate binary reads them
// per invocation.
type GateConfig struct {
	// GateBinary is the absolute path to the gate binary.
	// Required when Gate != nil.
	GateBinary string

	// TempDirRoot is where the per-spawn settings.json lives. If
	// empty, `<Layout.SessionDir(id)>/gate` is used.
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
		spawner = claude.Spawner{BypassPermissions: bypassPerms}
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

// attachGateConfig writes the per-spawn settings.json (claude's
// `--settings` file pointing at the gate binary) and returns a
// wrapped spawner.
//
// Rules + AutoApproved are NOT written here anymore — they live in
// the shared spec at gate.SharedSpecPath(AppName), populated by the
// daemon at boot and rewritten on always-allow / revoke. The gate
// binary reads the shared spec at every invocation.
//
// No env vars are injected — gate derives all paths from its
// compile-time AppName (set via -ldflags by `wick build`).
func (f *ClaudeFactory) attachGateConfig(opt FactoryOptions, base provider.Spawner, cfg *GateConfig) (provider.Spawner, error) {
	root := cfg.TempDirRoot
	if root == "" {
		root = filepath.Join(f.Layout.SessionDir(opt.SessionID), "gate")
	}
	settingsPath, err := gate.WriteClaudeSettings(root, cfg.GateBinary)
	if err != nil {
		return base, err
	}

	// If the underlying spawner is real claude, push the settings
	// path into a fresh copy.
	if cs, ok := base.(claude.Spawner); ok {
		cs.SettingsPath = settingsPath
		base = cs
	}
	return base, nil
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
