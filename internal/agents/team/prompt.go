package team

import (
	"context"

	"github.com/yogasw/wick/internal/agents/project"
	systemprompt "github.com/yogasw/wick/internal/agents/system-prompt"
	"github.com/yogasw/wick/internal/entity"
)

// PromptFor is the Team part of a spawn's system prompt. A Team agent's
// own session gets the Team overlay plus its "Who you are" block, built
// from the agent's row every spawn whatever the persona says; a
// sub-agent delegated under one gets a single line naming whom it works
// for; any other session gets "".
func (s *Service) PromptFor(ctx context.Context, sessionID string, subAgent bool) string {
	p := s.AgentFor(ctx, sessionID)
	if p == nil {
		return ""
	}
	if subAgent {
		return SubAgentOfTeam(p.Handle)
	}
	var others []Member
	if all, err := s.List(ctx, p.OwnerUserID); err == nil {
		for _, o := range all {
			// A disabled agent cannot be reached, so it is not offered.
			if o.ID != p.ID && !o.Disabled {
				others = append(others, s.memberOf(o))
			}
		}
	}
	return systemprompt.ImmutableTeam() + "\n\n" + WhoYouAre(s.memberOf(*p), others)
}

// memberOf reads an agent's name and description off its project; a
// missing project leaves the handle as the name.
func (s *Service) memberOf(p entity.AgentPersona) Member {
	m := Member{Name: p.Handle, Handle: p.Handle, IsCaptain: p.IsCaptain}
	if p.ProjectID != "" {
		if proj, err := project.Load(s.layout, p.ProjectID); err == nil {
			if proj.Meta.Name != "" {
				m.Name = proj.Meta.Name
			}
			m.Description = proj.Meta.Description
		}
	}
	return m
}
