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

	// Gate (optional) attaches a command whitelist to every spawn.
	// When non-nil, Build writes a per-session settings.json + spec
	// file to a temp dir, points the spawner at the settings file,
	// and injects WICK_GATE_SPEC into ExtraEnv so wick-gate finds
	// its config. nil = no gate (fail-open, only safe for tests).
	Gate *GateConfig

	// SpawnLogger (optional) writes one jsonl per spawn under
	// `<base>/backends/spawns/`. Each spawn emits `start` on Build +
	// `exit` from the OnExit hook so the Backends UI can list spawn
	// history per backend by `ls`-ing the directory. nil = no logging.
	SpawnLogger *provider.SpawnLogger
}

// GateConfig describes the gate plumbing: where the wick-gate binary
// lives + what rules it enforces. The factory turns this into one
// {settings.json, spec.json} pair per spawn.
type GateConfig struct {
	// WickGateBinary is the absolute path to the wick-gate binary.
	// Required when Gate != nil.
	WickGateBinary string
	// Rules is the whitelist enforced for every spawn under this
	// factory. Future work may take rules per-session.
	Rules []gate.CommandRule
	// TempDirRoot is where per-spawn gate artifacts live. If empty,
	// `<Layout.SessionDir(id)>/gate` is used.
	TempDirRoot string
}

// Build returns a fresh agent + state machine + store wired for one
// session+agent. Caller (the pool) is responsible for calling
// agent.Start.
func (f *ClaudeFactory) Build(opt FactoryOptions) (*provider.Agent, *state.Machine, *store.Store, error) {
	st := state.New(nil)
	sto := store.New(store.Options{
		Layout:    f.Layout,
		SessionID: opt.SessionID,
		AgentName: opt.AgentName,
		RecordRaw: f.RecordRaw,
	})

	spawner := f.Spawner
	if spawner == nil {
		spawner = claude.Spawner{}
	}

	var extraEnv []string
	if f.Gate != nil {
		s, env, err := f.attachGate(opt, spawner)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("attach gate: %w", err)
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
		ParserFactory: func() event.Parser { return event.NewClaudeParser() },
		Spawner:       gateAwareSpawner{inner: spawner, extraEnv: extraEnv},
		Store:         sto,
		State:         st,
		OnEvent:       onEvent,
		OnExit:        onExit,
	})
	return a, st, sto, nil
}

// attachGate writes the per-spawn settings.json + spec.json under
// <gate-dir>/<sessionID>/ and returns:
//
//   - a wrapped Spawner that forces ClaudeSpawner.SettingsPath when
//     the underlying spawner is a real claude.Spawner; for fake
//     spawners the settings are still written (so tests can read
//     them) but ignored
//   - the env-var slice ([WICK_GATE_SPEC=<path>]) the spawner adds
//     to its subprocess
func (f *ClaudeFactory) attachGate(opt FactoryOptions, base provider.Spawner) (provider.Spawner, []string, error) {
	root := f.Gate.TempDirRoot
	if root == "" {
		root = filepath.Join(f.Layout.SessionDir(opt.SessionID), "gate")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return base, nil, err
	}
	spec := gate.Spec{
		SessionID: opt.SessionID,
		AgentName: opt.AgentName,
		Layout: gate.SpecLayout{
			SessionCommandsPath: f.Layout.SessionCommands(opt.SessionID),
		},
		Rules: f.Gate.Rules,
	}
	settingsPath, specPath, err := gate.WriteSpawnArtifacts(root, spec, f.Gate.WickGateBinary)
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
