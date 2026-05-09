package registry

import (
	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/preset"
	"github.com/yogasw/wick/internal/agents/workspace"
)

// Bootstrap is the canonical boot sequence: ensure layout, seed default
// preset + default workspace, scan disk into the registry. Call once at
// process start. Returns a Manager ready to serve traffic.
func Bootstrap(layout config.Layout) (*Manager, error) {
	if err := layout.EnsureLayout(); err != nil {
		return nil, err
	}
	if err := preset.EnsureDefault(layout); err != nil {
		return nil, err
	}
	if err := workspace.EnsureDefault(layout); err != nil {
		return nil, err
	}
	reg := New(layout)
	if err := reg.Reload(); err != nil {
		return nil, err
	}
	return NewManager(reg), nil
}
