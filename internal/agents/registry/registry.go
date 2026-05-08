// Package registry holds the in-memory cache of on-disk Agents state
// (Registry) plus a mutator wrapper (Manager) and the boot entrypoint
// (Bootstrap). Files in this package:
//
//   - registry.go  — Registry: read-only cache, Reload(), accessors
//   - manager.go   — Manager: disk mutate + cache refresh
//   - bootstrap.go — Bootstrap: ensure layout + default preset, load cache
//
// Files remain the source of truth — the registry is a cache populated
// at boot via Reload() and refreshed on demand by Manager mutators.
// Filesystem watching is intentionally out of scope; mutation paths
// refresh the relevant entry inline.
package registry

import (
	"sort"
	"sync"

	"github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/preset"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
)

// Registry is the in-memory view of the on-disk agents state.
//
// Concurrency: protected by RWMutex. Read-heavy access patterns (UI
// listings, dashboard) hold RLock; mutators hold Lock.
type Registry struct {
	layout config.Layout

	mu       sync.RWMutex
	projects map[string]project.Project
	sessions map[string]session.Session
	presets  map[string]struct{}
}

// New returns an empty registry bound to the given layout. Call
// Reload() before serving traffic.
func New(layout config.Layout) *Registry {
	return &Registry{
		layout:   layout,
		projects: map[string]project.Project{},
		sessions: map[string]session.Session{},
		presets:  map[string]struct{}{},
	}
}

// Layout returns the underlying layout. Read-only.
func (r *Registry) Layout() config.Layout { return r.layout }

// Reload re-scans every folder and rebuilds the in-memory maps.
// Designed to be safe to call at any time: takes the write lock,
// replaces maps wholesale. On boot, also resets each session's status
// to idle (any subprocess from the previous run is dead).
func (r *Registry) Reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err := r.layout.EnsureLayout(); err != nil {
		return err
	}

	presetNames, err := preset.List(r.layout)
	if err != nil {
		return err
	}
	presets := make(map[string]struct{}, len(presetNames))
	for _, n := range presetNames {
		presets[n] = struct{}{}
	}

	projectNames, err := project.List(r.layout)
	if err != nil {
		return err
	}
	projects := make(map[string]project.Project, len(projectNames))
	for _, n := range projectNames {
		p, err := project.Load(r.layout, n)
		if err != nil {
			// Skip unreadable folders rather than fail the whole boot.
			// A broken project shouldn't prevent the rest of wick from
			// running — operator can fix it manually.
			continue
		}
		projects[n] = p
	}

	sessionIDs, err := session.List(r.layout)
	if err != nil {
		return err
	}
	sessions := make(map[string]session.Session, len(sessionIDs))
	for _, id := range sessionIDs {
		s, err := session.Load(r.layout, id)
		if err != nil {
			continue
		}
		// Subprocess from previous run is gone — force status to idle
		// and zero per-agent statuses. cli_session_id is preserved for
		// resume.
		dirty := false
		if s.Meta.Status != session.StatusIdle {
			s.Meta.Status = session.StatusIdle
			dirty = true
		}
		for i := range s.Agents {
			if s.Agents[i].Status != "idle" {
				s.Agents[i].Status = "idle"
				dirty = true
			}
		}
		if dirty {
			_ = session.SaveMeta(r.layout, id, s.Meta)
			_ = session.SaveAgents(r.layout, id, s.Agents)
		}
		sessions[id] = s
	}

	r.projects = projects
	r.sessions = sessions
	r.presets = presets
	return nil
}

// Projects returns a snapshot copy of the projects map. Callers that
// just need names should prefer ProjectNames to avoid copying meta.
func (r *Registry) Projects() map[string]project.Project {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]project.Project, len(r.projects))
	for k, v := range r.projects {
		out[k] = v
	}
	return out
}

// ProjectNames returns sorted project names.
func (r *Registry) ProjectNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.projects))
	for k := range r.projects {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Project returns one project by name, ok=false if missing.
func (r *Registry) Project(name string) (project.Project, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.projects[name]
	return p, ok
}

// Sessions returns a snapshot copy.
func (r *Registry) Sessions() map[string]session.Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]session.Session, len(r.sessions))
	for k, v := range r.sessions {
		out[k] = v
	}
	return out
}

// SessionIDs returns IDs sorted by last_active descending — the order
// listing pages want by default.
func (r *Registry) SessionIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	type kv struct {
		id string
		s  session.Session
	}
	all := make([]kv, 0, len(r.sessions))
	for k, v := range r.sessions {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].s.Meta.LastActive.After(all[j].s.Meta.LastActive)
	})
	out := make([]string, len(all))
	for i, kv := range all {
		out[i] = kv.id
	}
	return out
}

// Session returns one session by ID, ok=false if missing.
func (r *Registry) Session(id string) (session.Session, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.sessions[id]
	return s, ok
}

// PresetNames returns sorted preset names.
func (r *Registry) PresetNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.presets))
	for k := range r.presets {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// HasPreset reports whether the registry knows about a preset.
func (r *Registry) HasPreset(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.presets[name]
	return ok
}

// upsert / delete helpers used by Manager (same package). Callers must
// not hold the read lock when invoking these.

func (r *Registry) upsertProject(p project.Project) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.projects[p.Name] = p
}

func (r *Registry) upsertSession(s session.Session) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[s.ID] = s
}

func (r *Registry) upsertPreset(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.presets[name] = struct{}{}
}

func (r *Registry) deleteProject(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.projects, name)
}

func (r *Registry) deleteSession(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, id)
}

func (r *Registry) deletePreset(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.presets, name)
}
