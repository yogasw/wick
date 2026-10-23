package registry

import (
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
)

// ForgetProject drops a project and the given sessions from the cache
// without touching disk: the project trash has already moved their
// folders away, and every list must stop showing them at once.
func (m *Manager) ForgetProject(id string, sessionIDs []string) {
	for _, sid := range sessionIDs {
		m.reg.deleteSession(sid)
	}
	m.reg.deleteProject(id)
}

// AdoptRestored caches a project and its sessions after the trash moved
// their folders back. A session that no longer loads is skipped and
// returned, so the caller can report it instead of failing the restore.
func (m *Manager) AdoptRestored(id string, sessionIDs []string) (project.Project, []string, error) {
	p, err := project.Load(m.reg.layout, id)
	if err != nil {
		return project.Project{}, nil, err
	}
	m.reg.upsertProject(p)
	var missing []string
	for _, sid := range sessionIDs {
		s, err := session.Load(m.reg.layout, sid)
		if err != nil {
			missing = append(missing, sid)
			continue
		}
		m.reg.upsertSession(s)
	}
	return p, missing, nil
}
