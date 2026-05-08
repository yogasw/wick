package pool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yogasw/wick/internal/agents/agent"
	"github.com/yogasw/wick/internal/agents/agent/claude"
	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/event"
	"github.com/yogasw/wick/internal/agents/gate"
	"github.com/yogasw/wick/internal/agents/state"
	"github.com/yogasw/wick/internal/agents/store"
)

// ClaudeFactory is the production AgentFactory: wires a ClaudeParser +
// ClaudeSpawner into a fresh agent.Agent for each Build call.
//
// The factory owns no per-spawn state; the pool calls Build once per
// session activation.
type ClaudeFactory struct {
	Layout    config.Layout
	Spawner   agent.Spawner // optional override; nil = real claude
	RecordRaw bool
	OnEvent   func(sessionID, agentName string, ev event.AgentEvent)
	OnExit    func(sessionID, agentName string, reason agent.ExitReason)

	// Gate (optional) attaches a command whitelist to every spawn.
	// When non-nil, Build writes a per-session settings.json + spec
	// file to a temp dir, points the spawner at the settings file,
	// and injects WICK_GATE_SPEC into ExtraEnv so wick-gate finds
	// its config. nil = no gate (fail-open, only safe for tests).
	Gate *GateConfig
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
func (f *ClaudeFactory) Build(opt FactoryOptions) (*agent.Agent, *state.Machine, *store.Store, error) {
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
	var onExit func(agent.ExitReason)
	if f.OnExit != nil {
		sid, name := opt.SessionID, opt.AgentName
		onExit = func(r agent.ExitReason) { f.OnExit(sid, name, r) }
	}

	a := agent.New(agent.Options{
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
func (f *ClaudeFactory) attachGate(opt FactoryOptions, base agent.Spawner) (agent.Spawner, []string, error) {
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
	inner    agent.Spawner
	extraEnv []string
}

func (g gateAwareSpawner) Spawn(ctx context.Context, opt agent.SpawnOptions) (agent.Process, error) {
	opt.ExtraEnv = append(append([]string(nil), opt.ExtraEnv...), g.extraEnv...)
	return g.inner.Spawn(ctx, opt)
}
