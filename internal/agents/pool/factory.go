package pool

import (
	"context"
	"fmt"
	"os"
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

	// Gate (optional) attaches a static command whitelist to every spawn.
	// When non-nil, Build writes a per-session settings.json + spec
	// file to a temp dir, points the spawner at the settings file,
	// and injects GATE_SPEC into ExtraEnv so the gate binary finds
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

// GateConfig describes the gate plumbing: where the gate binary
// lives + what rules it enforces. The factory turns this into one
// {settings.json, spec.json} pair per spawn.
type GateConfig struct {
	// GateBinary is the absolute path to the gate binary.
	// Required when Gate != nil.
	GateBinary string
	// Rules is the whitelist enforced for every spawn under this
	// factory. Future work may take rules per-session.
	Rules []gate.CommandRule
	// TempDirRoot is where per-spawn gate artifacts (settings.json +
	// spec.json) live. If empty, `<Layout.SessionDir(id)>/gate` is
	// used.
	TempDirRoot string

	// SocketDir is the static (single-session-test) socket dir. The
	// factory writes `<SocketDir>/gate.sock` into spec.SocketPath so
	// the gate binary knows where to dial. Empty = no interactive
	// approval (whitelist-only). Production wires SocketDirFor
	// instead so each session gets its own socket path.
	SocketDir string

	// SocketDirFor (preferred over SocketDir for production) returns
	// the socket dir for a specific session. Factory calls this once
	// per Build, falling back to SocketDir when the func is nil.
	// Daemon must listen on `<SocketDirFor(sid)>/gate.sock` before
	// the spawn — pool's OnLifecycle "spawning" hook is the right
	// trigger.
	SocketDirFor func(sessionID string) string

	// AutoApprovedFor returns the list of "always allow" matchKey
	// hashes for a given session. Called once per Build to populate
	// spec.AutoApproved so the gate binary can short-circuit without
	// dialing the socket. nil = no auto-approves.
	AutoApprovedFor func(sessionID string) []string
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

	var extraEnv []string
	if activeGate != nil {
		s, env, err := f.attachGateConfig(opt, spawner, activeGate)
		if err != nil {
			return BuildResult{}, fmt.Errorf("attach gate: %w", err)
		}
		spawner = s
		extraEnv = env
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
		Spawner:       gateAwareSpawner{inner: spawner, extraEnv: extraEnv},
		Store:         sto,
		State:         st,
		OnEvent:       onEvent,
		OnExit:        onExit,
	})
	return BuildResult{Agent: a, State: st, Store: sto, OnStarted: onStarted}, nil
}

// attachGate writes the per-spawn settings.json + spec.json under
// <gate-dir>/<sessionID>/ and returns:
//
//   - a wrapped Spawner that forces ClaudeSpawner.SettingsPath when
//     the underlying spawner is a real claude.Spawner; for fake
//     spawners the settings are still written (so tests can read
//     them) but ignored
//   - the env-var slice ([GATE_SPEC=<path>]) the spawner adds
//     to its subprocess
func (f *ClaudeFactory) attachGateConfig(opt FactoryOptions, base provider.Spawner, cfg *GateConfig) (provider.Spawner, []string, error) {
	root := cfg.TempDirRoot
	if root == "" {
		root = filepath.Join(f.Layout.SessionDir(opt.SessionID), "gate")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return base, nil, err
	}
	sockDir := cfg.SocketDir
	if cfg.SocketDirFor != nil {
		sockDir = cfg.SocketDirFor(opt.SessionID)
	}
	socketPath := ""
	if sockDir != "" {
		// Socket file lives one level above the per-spawn artifact
		// dir so multiple spawns under the same session share one
		// listener (created by the daemon at session start).
		socketPath = filepath.Join(sockDir, "gate.sock")
	}
	var autoApproved []string
	if cfg.AutoApprovedFor != nil {
		autoApproved = cfg.AutoApprovedFor(opt.SessionID)
	}
	spec := gate.Spec{
		SessionID: opt.SessionID,
		AgentName: opt.AgentName,
		Layout: gate.SpecLayout{
			SessionCommandsPath: f.Layout.SessionCommands(opt.SessionID),
		},
		Rules:        cfg.Rules,
		SocketPath:   socketPath,
		AutoApproved: autoApproved,
	}
	settingsPath, specPath, err := gate.WriteSpawnArtifacts(root, spec, cfg.GateBinary)
	if err != nil {
		return base, nil, err
	}

	// If the underlying spawner is real claude, push the settings
	// path into a fresh copy. We can't mutate the existing struct
	// directly (it's a value), so swap with a configured one.
	if cs, ok := base.(claude.Spawner); ok {
		cs.SettingsPath = settingsPath
		base = cs
	}
	return base, []string{
		gate.HookEnvVar + "=" + specPath,
	}, nil
}

// gateAwareSpawner wraps a Spawner so the gate's ExtraEnv lands in
// every Spawn call without the underlying spawner having to know
// about gate.
type gateAwareSpawner struct {
	inner    provider.Spawner
	extraEnv []string
}

func (g gateAwareSpawner) Spawn(ctx context.Context, opt provider.SpawnOptions) (provider.Process, error) {
	opt.ExtraEnv = append(append([]string(nil), opt.ExtraEnv...), g.extraEnv...)
	return g.inner.Spawn(ctx, opt)
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
