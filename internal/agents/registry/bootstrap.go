package registry

import (
	"github.com/google/uuid"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/preset"
	"github.com/yogasw/wick/internal/agents/project"
)

// Bootstrap is the canonical boot sequence: ensure layout, seed default
// preset, migrate legacy workspaces → projects (idempotent), ensure a
// default project exists, scan disk into the registry. Call once at
// process start. Returns a Manager ready to serve traffic.
func Bootstrap(layout config.Layout) (*Manager, error) {
	if err := layout.EnsureLayout(); err != nil {
		return nil, err
	}
	if err := preset.EnsureDefault(layout); err != nil {
		return nil, err
	}
	// One-shot, idempotent migration: existing workspaces become projects
	// and sessions are relinked. No-op if any project already exists.
	if err := project.MigrateWorkspacesToProjects(layout, uuid.NewString, func(wsName, projectID string) error {
		return project.RelinkSessions(layout, wsName, projectID)
	}); err != nil {
		return nil, err
	}
	// Fresh install (or empty after migration with no workspaces): seed
	// the built-in default project so session create always has a target.
	if err := project.EnsureDefault(layout, uuid.NewString); err != nil {
		return nil, err
	}
	reg := New(layout)
	if err := reg.Reload(); err != nil {
		return nil, err
	}
	return NewManager(reg), nil
}
