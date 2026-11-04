package api

import (
	agentconfig "github.com/yogasw/wick/internal/agents/config"
	"github.com/yogasw/wick/internal/agents/project"
	"github.com/yogasw/wick/internal/agents/session"
)

// auditPlaces resolves sessions and projects from the agents layout for
// the manager's audit pages (manager.AuditPlaces).
type auditPlaces struct {
	layout agentconfig.Layout
}

func (p auditPlaces) SessionProject(sessionID string) string {
	sess, err := session.Load(p.layout, sessionID)
	if err != nil {
		return ""
	}
	return sess.Meta.ProjectID
}

func (p auditPlaces) ProjectName(projectID string) string {
	pr, err := project.Load(p.layout, projectID)
	if err != nil {
		return ""
	}
	return pr.Meta.Name
}

func (p auditPlaces) SessionLabel(sessionID string) string {
	sess, err := session.Load(p.layout, sessionID)
	if err != nil {
		return ""
	}
	return sess.Meta.Label
}
