package registry

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/yogasw/wick/internal/agents/preset"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
)

// ProjectHookWriter writes (or removes) the gate hook config into a
// project's folder .claude/settings.local.json. Implemented by
// gate.hookConfigWriter; the interface keeps the registry package free
// of a direct gate import.
//
// Write must be idempotent — it is called on every CreateProject and
// MoveSession, even when the file already exists with the same content.
// Remove is best-effort: callers log but do not hard-fail when it errors.
type ProjectHookWriter interface {
	// Write installs the hook config at <folder>/.claude/settings.local.json.
	Write(folder, gateBin string) error
	// Remove deletes the hook config. Idempotent — missing file is not an error.
	Remove(folder string) error
}

// Manager wraps Registry with mutators that keep disk and memory in
// sync. Pure-disk functions in the project / session / preset
// packages stay usable on their own — Manager just glues a
// Reload-on-mutate strategy for callers that want one entry point.
// UI handlers and CLI commands use Manager; integration tests use
// the bare functions.
type Manager struct {
	reg *Registry
	// HookWriter, when non-nil, is called after every CreateProject and
	// MoveSession to ensure .claude/settings.local.json exists in the
	// target project folder. Nil = skip hook injection (tests, non-claude setups).
	HookWriter ProjectHookWriter
	// GateBinLoader returns the current gate binary path. Called on every
	// inject so live config changes take effect without a server restart.
	// Nil, or a loader that returns "", disables injection silently.
	GateBinLoader func() string
}

func NewManager(reg *Registry) *Manager { return &Manager{reg: reg} }

// injectHook resolves the project folder and writes the hook config into
// it. All outcomes — skip, success, failure — are logged so a missing
// .claude/settings.local.json is always traceable.
func (m *Manager) injectHook(_ context.Context, projectID string) {
	if m.HookWriter == nil {
		log.Debug().Str("project", projectID).Msg("registry.manager: hook injection skipped (no HookWriter configured)")
		return
	}
	if m.GateBinLoader == nil {
		log.Debug().Str("project", projectID).Msg("registry.manager: hook injection skipped (no GateBinLoader configured)")
		return
	}
	gateBin := m.GateBinLoader()
	if gateBin == "" {
		log.Warn().Str("project", projectID).Msg("registry.manager: hook injection skipped — GateBinLoader returned empty path")
		return
	}
	path, err := project.ResolvePath(m.reg.layout, projectID)
	if err != nil {
		log.Error().Err(err).Str("project", projectID).Msg("registry.manager: hook injection failed — cannot resolve project path")
		return
	}
	log.Debug().
		Str("project", projectID).
		Str("path", path).
		Str("gate_bin", gateBin).
		Msg("registry.manager: injecting hook config into project")
	if err := m.HookWriter.Write(path, gateBin); err != nil {
		log.Error().
			Err(err).
			Str("project", projectID).
			Str("path", path).
			Str("gate_bin", gateBin).
			Msg("registry.manager: hook injection failed — Write returned error")
		return
	}
	log.Info().
		Str("project", projectID).
		Str("path", path).
		Msg("registry.manager: hook config injected successfully")
}

// Registry exposes the underlying registry for read paths.
func (m *Manager) Registry() *Registry { return m.reg }

// CreateProject runs the disk create + refreshes registry cache, then
// injects the gate hook config into the new project folder.
func (m *Manager) CreateProject(ctx context.Context, opt project.CreateOptions) (project.Project, error) {
	log.Debug().Str("project", opt.Name).Str("custom_path", opt.CustomPath).Msg("registry.manager: creating project")
	p, err := project.Create(m.reg.layout, opt)
	if err != nil {
		log.Error().Err(err).Str("project", opt.Name).Msg("registry.manager: project create failed")
		return project.Project{}, err
	}
	log.Info().Str("project", p.Meta.ID).Str("name", opt.Name).Msg("registry.manager: project created")
	m.reg.upsertProject(p)
	m.injectHook(ctx, p.Meta.ID)
	return p, nil
}

// UpdateProject rewrites project meta + refreshes cache.
func (m *Manager) UpdateProject(_ context.Context, id string, meta project.Meta) (project.Project, error) {
	if err := project.SaveMeta(m.reg.layout, id, meta); err != nil {
		return project.Project{}, err
	}
	p, err := project.Load(m.reg.layout, id)
	if err != nil {
		return project.Project{}, err
	}
	m.reg.upsertProject(p)
	return p, nil
}

// DeleteProject removes the project metadata folder and unscopes
// dependent sessions (sets their ProjectID to ""). The project folder
// contents (managed `files/`) are deleted only for managed projects;
// custom paths are left untouched because wick never owned them.
func (m *Manager) DeleteProject(_ context.Context, id string) error {
	for sid, s := range m.reg.Sessions() {
		if s.Meta.ProjectID != id {
			continue
		}
		s.Meta.ProjectID = ""
		_ = session.SaveMeta(m.reg.layout, sid, s.Meta)
		m.reg.upsertSession(s)
	}
	if err := project.Delete(m.reg.layout, id); err != nil {
		return err
	}
	m.reg.deleteProject(id)
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

// RefreshSession reloads one session from disk and updates the in-memory
// registry cache. Use this after lower-level code mutates session files
// directly.
func (m *Manager) RefreshSession(id string) error {
	s, err := session.Load(m.reg.layout, id)
	if err != nil {
		return err
	}
	m.reg.upsertSession(s)
	return nil
}

// SubscribeUser opts userID in to lifecycle push notifications for the
// given session. Idempotent — calling twice with the same userID is a
// no-op. Returns true when the subscriber list actually changed and
// the meta file was rewritten, so callers can avoid unnecessary work.
//
// Sessions are shared across users (anyone can open them) but pushes
// target only the IDs in meta.Subscribers — this is how the system
// avoids paging users about sessions they don't care about.
func (m *Manager) SubscribeUser(id, userID string) (bool, error) {
	s, ok := m.reg.Session(id)
	if !ok {
		return false, fmt.Errorf("session %q not found", id)
	}
	if !s.Meta.AddSubscriber(userID) {
		return false, nil
	}
	if err := session.SaveMeta(m.reg.layout, id, s.Meta); err != nil {
		return false, err
	}
	m.reg.upsertSession(s)
	return true, nil
}

// UnsubscribeUser is the inverse of SubscribeUser. Returns true when
// the list changed.
func (m *Manager) UnsubscribeUser(id, userID string) (bool, error) {
	s, ok := m.reg.Session(id)
	if !ok {
		return false, fmt.Errorf("session %q not found", id)
	}
	if !s.Meta.RemoveSubscriber(userID) {
		return false, nil
	}
	if err := session.SaveMeta(m.reg.layout, id, s.Meta); err != nil {
		return false, err
	}
	m.reg.upsertSession(s)
	return true, nil
}

// DeleteSession removes the session folder + cache entry.
func (m *Manager) DeleteSession(ctx context.Context, id string) error {
	if err := session.Delete(ctx, m.reg.layout, id); err != nil {
		return err
	}
	m.reg.deleteSession(id)
	return nil
}

// MoveSession moves a session to a different project + refreshes cache,
// then injects the gate hook config into the target project folder so
// the next spawn picks up a correctly-configured .claude/settings.local.json.
// Empty newProjectID unscopes the session.
func (m *Manager) MoveSession(ctx context.Context, id, newProjectID string) error {
	log.Debug().Str("session", id).Str("project", newProjectID).Msg("registry.manager: moving session to project")
	if err := session.SetProject(ctx, m.reg.layout, id, newProjectID); err != nil {
		log.Error().Err(err).Str("session", id).Str("project", newProjectID).Msg("registry.manager: session move failed")
		return err
	}
	s, err := session.Load(m.reg.layout, id)
	if err != nil {
		return err
	}
	m.reg.upsertSession(s)
	log.Info().Str("session", id).Str("project", newProjectID).Msg("registry.manager: session moved")
	if newProjectID != "" {
		m.injectHook(ctx, newProjectID)
	} else {
		log.Debug().Str("session", id).Msg("registry.manager: hook injection skipped — project unbound (empty)")
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
