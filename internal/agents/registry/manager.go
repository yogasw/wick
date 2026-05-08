package registry

import (
	"context"

	"github.com/yogasw/wick/internal/agents/preset"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/storage"
)

// Manager wraps Registry with mutators that keep disk and memory in
// sync. Pure-disk functions in the project / session / preset packages
// stay usable on their own — Manager just glues a Reload-on-mutate
// strategy for callers that want one entry point. UI handlers and CLI
// commands use Manager; integration tests use the bare functions.
type Manager struct {
	reg *Registry
}

func NewManager(reg *Registry) *Manager { return &Manager{reg: reg} }

// Registry exposes the underlying registry for read paths.
func (m *Manager) Registry() *Registry { return m.reg }

// CreateProject runs the disk create + refreshes registry cache.
func (m *Manager) CreateProject(ctx context.Context, opt project.CreateOptions) (project.Project, error) {
	p, err := project.Create(ctx, m.reg.layout, opt)
	if err != nil {
		return project.Project{}, err
	}
	m.reg.upsertProject(p)
	return p, nil
}

// DeleteProject removes the project and detaches dependent sessions.
// We iterate the registry rather than the disk so we don't re-scan
// during a delete; sessions still on disk but missing from the
// registry would be left dangling, but Reload() at boot fixes that.
func (m *Manager) DeleteProject(ctx context.Context, name string) error {
	for id, s := range m.reg.Sessions() {
		if s.Meta.Project != name {
			continue
		}
		if storage.PathExists(m.reg.layout.SessionWorkspace(id)) {
			// Best-effort worktree removal — the project deletion
			// below is the actual cleanup.
			_ = removeSessionWorktree(ctx, m.reg, name, id)
		}
		// Detach the session from the deleted project but keep the
		// session itself — the user can re-attach to another project.
		s.Meta.Project = ""
		_ = session.SaveMeta(m.reg.layout, id, s.Meta)
		m.reg.upsertSession(s)
	}
	if err := project.Delete(m.reg.layout, name); err != nil {
		return err
	}
	m.reg.deleteProject(name)
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

// DeleteSession removes the session worktree + folder + cache entry.
func (m *Manager) DeleteSession(ctx context.Context, id string) error {
	if err := session.Delete(ctx, m.reg.layout, id); err != nil {
		return err
	}
	m.reg.deleteSession(id)
	return nil
}

// SwitchProject moves a session to a new project + refreshes cache.
func (m *Manager) SwitchProject(ctx context.Context, id, newProject string) error {
	if err := session.SwitchProject(ctx, m.reg.layout, id, newProject); err != nil {
		return err
	}
	s, err := session.Load(m.reg.layout, id)
	if err != nil {
		return err
	}
	m.reg.upsertSession(s)
	return nil
}

// AddAgent + cache refresh.
func (m *Manager) AddAgent(id, name, backend string) error {
	if err := session.AddAgent(m.reg.layout, id, name, backend); err != nil {
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

// removeSessionWorktree shells out via the project pkg helper. We
// duplicate the path math the session pkg uses internally because
// session.Delete is the only caller of session.removeWorktree there
// and we don't want to re-export it just for this fallback path.
func removeSessionWorktree(ctx context.Context, reg *Registry, projectName, id string) error {
	master := reg.layout.ProjectWorkspace(projectName)
	worktree := reg.layout.SessionWorkspace(id)
	return project.RemoveWorktree(ctx, master, worktree)
}
