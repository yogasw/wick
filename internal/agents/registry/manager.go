package registry

import (
	"context"

	"github.com/yogasw/wick/internal/agents/preset"
	"github.com/yogasw/wick/internal/agents/session"
	"github.com/yogasw/wick/internal/agents/workspace"
)

// Manager wraps Registry with mutators that keep disk and memory in
// sync. Pure-disk functions in the workspace / session / preset
// packages stay usable on their own — Manager just glues a
// Reload-on-mutate strategy for callers that want one entry point.
// UI handlers and CLI commands use Manager; integration tests use
// the bare functions.
type Manager struct {
	reg *Registry
}

func NewManager(reg *Registry) *Manager { return &Manager{reg: reg} }

// Registry exposes the underlying registry for read paths.
func (m *Manager) Registry() *Registry { return m.reg }

// CreateWorkspace runs the disk create + refreshes registry cache.
func (m *Manager) CreateWorkspace(_ context.Context, opt workspace.CreateOptions) (workspace.Workspace, error) {
	w, err := workspace.Create(m.reg.layout, opt)
	if err != nil {
		return workspace.Workspace{}, err
	}
	m.reg.upsertWorkspace(w)
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

// DeleteSession removes the session folder + cache entry.
func (m *Manager) DeleteSession(ctx context.Context, id string) error {
	if err := session.Delete(ctx, m.reg.layout, id); err != nil {
		return err
	}
	m.reg.deleteSession(id)
	return nil
}

// SwitchWorkspace moves a session to a different workspace + refreshes
// cache. No filesystem work is done — the next agent spawn picks up
// the new cwd.
func (m *Manager) SwitchWorkspace(ctx context.Context, id, newWorkspace string) error {
	if err := session.SwitchWorkspace(ctx, m.reg.layout, id, newWorkspace); err != nil {
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
