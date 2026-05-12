package registry

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/agents/preset"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/workspace"
)

// WorkspaceHookWriter writes (or removes) the gate hook config into a
// workspace directory's .claude/settings.local.json. Implemented by
// gate.hookConfigWriter; the interface keeps the registry package free
// of a direct gate import.
//
// Write must be idempotent — it is called on every CreateWorkspace and
// SwitchWorkspace, even when the file already exists with the same content.
// Remove is best-effort: callers log but do not hard-fail when it errors.
type WorkspaceHookWriter interface {
	// Write installs the hook config at <workspace>/.claude/settings.local.json.
	Write(workspace, gateBin string) error
	// Remove deletes the hook config. Idempotent — missing file is not an error.
	Remove(workspace string) error
}

// Manager wraps Registry with mutators that keep disk and memory in
// sync. Pure-disk functions in the workspace / session / preset
// packages stay usable on their own — Manager just glues a
// Reload-on-mutate strategy for callers that want one entry point.
// UI handlers and CLI commands use Manager; integration tests use
// the bare functions.
type Manager struct {
	reg *Registry
	// HookWriter, when non-nil, is called after every CreateWorkspace and
	// SwitchWorkspace to ensure .claude/settings.local.json exists in the
	// target workspace. Nil = skip hook injection (tests, non-claude setups).
	HookWriter WorkspaceHookWriter
	// GateBinLoader returns the current gate binary path. Called on every
	// inject so live config changes take effect without a server restart.
	// Nil, or a loader that returns "", disables injection silently.
	GateBinLoader func() string
}

func NewManager(reg *Registry) *Manager { return &Manager{reg: reg} }

// injectHook resolves the workspace path and writes the hook config into
// it. All outcomes — skip, success, failure — are logged so a missing
// .claude/settings.local.json is always traceable.
func (m *Manager) injectHook(ctx context.Context, workspaceName string) {
	if m.HookWriter == nil {
		log.Debug().Str("workspace", workspaceName).Msg("registry.manager: hook injection skipped (no HookWriter configured)")
		return
	}
	if m.GateBinLoader == nil {
		log.Debug().Str("workspace", workspaceName).Msg("registry.manager: hook injection skipped (no GateBinLoader configured)")
		return
	}
	gateBin := m.GateBinLoader()
	if gateBin == "" {
		log.Warn().Str("workspace", workspaceName).Msg("registry.manager: hook injection skipped — GateBinLoader returned empty path")
		return
	}
	path, err := workspace.ResolvePath(m.reg.layout, workspaceName)
	if err != nil {
		log.Error().Err(err).Str("workspace", workspaceName).Msg("registry.manager: hook injection failed — cannot resolve workspace path")
		return
	}
	log.Debug().
		Str("workspace", workspaceName).
		Str("path", path).
		Str("gate_bin", gateBin).
		Msg("registry.manager: injecting hook config into workspace")
	if err := m.HookWriter.Write(path, gateBin); err != nil {
		log.Error().
			Err(err).
			Str("workspace", workspaceName).
			Str("path", path).
			Str("gate_bin", gateBin).
			Msg("registry.manager: hook injection failed — Write returned error")
		return
	}
	log.Info().
		Str("workspace", workspaceName).
		Str("path", path).
		Msg("registry.manager: hook config injected successfully")
}

// Registry exposes the underlying registry for read paths.
func (m *Manager) Registry() *Registry { return m.reg }

// CreateWorkspace runs the disk create + refreshes registry cache, then
// injects the gate hook config into the new workspace directory.
func (m *Manager) CreateWorkspace(ctx context.Context, opt workspace.CreateOptions) (workspace.Workspace, error) {
	log.Debug().Str("workspace", opt.Name).Str("custom_path", opt.CustomPath).Msg("registry.manager: creating workspace")
	w, err := workspace.Create(m.reg.layout, opt)
	if err != nil {
		log.Error().Err(err).Str("workspace", opt.Name).Msg("registry.manager: workspace create failed")
		return workspace.Workspace{}, err
	}
	log.Info().Str("workspace", opt.Name).Msg("registry.manager: workspace created")
	m.reg.upsertWorkspace(w)
	m.injectHook(ctx, opt.Name)
	return w, nil
}

// DeleteWorkspace removes the workspace metadata folder and detaches
// dependent sessions. The workspace folder contents (managed `files/`
// or the user's custom path) are deleted only for managed workspaces;
// custom paths are left untouched because wick never owned them.
func (m *Manager) DeleteWorkspace(_ context.Context, name string) error {
	for id, s := range m.reg.Sessions() {
		if s.Meta.Workspace != name {
			continue
		}
		s.Meta.Workspace = ""
		_ = session.SaveMeta(m.reg.layout, id, s.Meta)
		m.reg.upsertSession(s)
	}
	if err := workspace.Delete(m.reg.layout, name); err != nil {
		return err
	}
	m.reg.deleteWorkspace(name)
	return nil
}

// CreateSession runs disk create + cache refresh.
func (m *Manager) CreateSession(ctx context.Context, opt session.CreateOptions) (session.Session, error) {
	s, err := session.Create(ctx, m.reg.layout, opt)
	if err != nil {
		return session.Session{}, err
	}
	m.reg.upsertSession(s)
	return s, nil
}

// Register adds or updates a session in the in-memory registry without
// touching disk. Use this when a session was created externally (e.g. by
// the pool's auto-create path for Slack/channel sessions) so the dashboard
// sees it immediately without a full Reload.
func (m *Manager) Register(s session.Session) {
	m.reg.upsertSession(s)
}

// DeleteSession removes the session folder + cache entry.
func (m *Manager) DeleteSession(ctx context.Context, id string) error {
	if err := session.Delete(ctx, m.reg.layout, id); err != nil {
		return err
	}
	m.reg.deleteSession(id)
	return nil
}

// SwitchWorkspace moves a session to a different workspace + refreshes
// cache, then injects the gate hook config into the target workspace so
// the next spawn picks up a correctly-configured .claude/settings.local.json.
func (m *Manager) SwitchWorkspace(ctx context.Context, id, newWorkspace string) error {
	log.Debug().Str("session", id).Str("workspace", newWorkspace).Msg("registry.manager: switching workspace")
	if err := session.SwitchWorkspace(ctx, m.reg.layout, id, newWorkspace); err != nil {
		log.Error().Err(err).Str("session", id).Str("workspace", newWorkspace).Msg("registry.manager: workspace switch failed")
		return err
	}
	s, err := session.Load(m.reg.layout, id)
	if err != nil {
		return err
	}
	m.reg.upsertSession(s)
	log.Info().Str("session", id).Str("workspace", newWorkspace).Msg("registry.manager: workspace switched")
	if newWorkspace != "" {
		m.injectHook(ctx, newWorkspace)
	} else {
		log.Debug().Str("session", id).Msg("registry.manager: hook injection skipped — workspace unbound (empty)")
	}
	return nil
}

// AddAgent + cache refresh.
func (m *Manager) AddAgent(id, name, provider string) error {
	if err := session.AddAgent(m.reg.layout, id, name, provider); err != nil {
		return err
	}
	s, err := session.Load(m.reg.layout, id)
	if err != nil {
		return err
	}
	m.reg.upsertSession(s)
	return nil
}

// SetActiveAgent + cache refresh.
func (m *Manager) SetActiveAgent(id, name string) error {
	if err := session.SetActiveAgent(m.reg.layout, id, name); err != nil {
		return err
	}
	s, err := session.Load(m.reg.layout, id)
	if err != nil {
		return err
	}
	m.reg.upsertSession(s)
	return nil
}

// CreatePreset + cache.
func (m *Manager) CreatePreset(name, body string) error {
	if err := preset.Create(m.reg.layout, name, body); err != nil {
		return err
	}
	m.reg.upsertPreset(name)
	return nil
}

// UpdatePreset rewrites without changing the registry membership.
func (m *Manager) UpdatePreset(name, body string) error {
	return preset.Update(m.reg.layout, name, body)
}

// DeletePreset + cache.
func (m *Manager) DeletePreset(name string) error {
	if err := preset.Delete(m.reg.layout, name); err != nil {
		return err
	}
	m.reg.deletePreset(name)
	return nil
}
